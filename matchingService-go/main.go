package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

type playerStruct struct {
	ID        string          `json:"id"`
	MatchedID string          `json:"matched_id"`
	IsHost    bool            `json:"is_host"`
	Ws        *websocket.Conn `json:"-"`
}

type roomStruct struct {
	Code string `json:"room_code"`
}

type roomCheckResponseMessage struct {
	IsValid bool `json:"is_valid"`
}

func (p playerStruct) match(mp *playerStruct, matchID uint64) *playerStruct {
	match := strconv.FormatUint(matchID, 10)
	mp.ID = match + "_host"
	p.ID = match + "_client"
	mp.MatchedID = p.ID
	p.MatchedID = mp.ID
	err1 := p.Ws.WriteJSON(p)
	if err1 != nil {
		p.Ws.Close()
		return mp
	}
	err2 := mp.Ws.WriteJSON(&mp)
	if err2 != nil {
		mp.Ws.Close()
		return &p
	}
	p.Ws.Close()
	mp.Ws.Close()
	return nil
}

func setupPublicMatches() {
	var client *playerStruct = nil
	var host *playerStruct = nil
	for {
		if client == nil {
			client = <-publicClients
		}
		if host == nil {
			host = <-publicClients
		}
		host.IsHost = true
		unMatchedPlayer := client.match(host, atomic.AddUint64(&id, 1))
		if unMatchedPlayer == nil {
			log.Println("Successfully setup a public match!")
		} else {
			if strings.Contains(unMatchedPlayer.ID, "host") {
				host = unMatchedPlayer
				client = nil
			} else {
				host = nil
				client = unMatchedPlayer
			}
		}
	}
}

var privateClients sync.Map
var registeredClients sync.Map
var publicClients = make(chan *playerStruct, 1000)
var upgrader = websocket.Upgrader{}
var id uint64

const letterBytes = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const (
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

var src = rand.NewSource(time.Now().UnixNano())

func randStringBytesMaskImprSrcSB(n int) string {
	sb := strings.Builder{}
	sb.Grow(n)
	// A src.Int63() generates 63 random bits, enough for letterIdxMax characters!
	for i, cache, remain := n-1, src.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = src.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			sb.WriteByte(letterBytes[idx])
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return sb.String()
}

func listenWebSocketConn(conn *websocket.Conn, sendConn *websocket.Conn, isConnHost bool, loginID string) {
	timeout := time.Time{}
	conn.SetReadDeadline(timeout)
	sendConn.SetWriteDeadline(timeout)
	var message interface{}
	messagesSent := 0
	for {
		err := conn.ReadJSON(&message)
		if err != nil {
			log.Println(err)
			break
		}
		// TODO: add validation before forwarding a message
		err = sendConn.WriteJSON(&message)
		if err != nil {
			log.Println(err)
			break
		}
		log.Println(loginID + ": forwarded a message")
		messagesSent++
	}
	log.Println("Timed-out/Done so closing connection for " + loginID)
	if isConnHost && messagesSent > 0 {
		registeredClients.Delete(loginID)
	}
}

func socketCloseHandler(code int, text string) error {
	log.Println("WebSocket closed with code: {} and message: "+text, code)
	return &websocket.CloseError{
		Code: code,
		Text: text,
	}
}

func main() {
	router := mux.NewRouter()
	router.HandleFunc("/", healthCheckHandler).Methods("GET")
	router.HandleFunc("/checkRoomCode", checkRoomCodeHandler).Methods("GET")
	router.HandleFunc("/publicMatch", publicMatchRequestHandler)
	router.HandleFunc("/privateMatch", privateMatchRequestHandler)
	router.HandleFunc("/rtcSetup", setupWebRTCConnHandler)

	log.Println("starting server at port: 8080")
	go setupPublicMatches()
	log.Fatal(http.ListenAndServe(":8080", router))
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "matching service running!")
}

func checkRoomCodeHandler(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code != "" {
		_, ok := privateClients.Load(code)
		resp := roomCheckResponseMessage{
			IsValid: ok,
		}
		b, err := json.Marshal(resp)
		if err != nil {
			http.Error(w, "could not encode", http.StatusBadRequest)
			return
		}
		w.Write(b)
		return
	}
	http.Error(w, "code not sent", http.StatusBadRequest)
}

func publicMatchRequestHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	ws.SetCloseHandler(socketCloseHandler)
	player := playerStruct{
		ID:        "",
		MatchedID: "",
		IsHost:    false,
		Ws:        ws,
	}
	log.Println("Pushing player to channel")
	publicClients <- &player
}

func setupWebRTCConnHandler(w http.ResponseWriter, r *http.Request) {
	loginID := r.Header.Get("login-id")
	s := strings.Split(loginID, "_")
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	ws.SetCloseHandler(socketCloseHandler)
	if s[1] == "client" {
		var hostPlayer interface{}
		var loaded bool
		seconds := 10
		hostName := s[0] + "_host"
		for {
			hostPlayer, loaded = registeredClients.Load(hostName)
			if !loaded {
				log.Println("Could not find the host: " + hostName)
				time.Sleep(time.Second * 1)
				seconds--
				if seconds == 0 {
					ws.Close()
					return
				}
				continue
			}
			break
		}
		host, ok := hostPlayer.(*websocket.Conn)
		if !ok {
			// TODO: return error saying something is wrong
			log.Println("Error converting host to websocket connection")
		}
		log.Println("Found Peers!: starting to listen")
		go listenWebSocketConn(ws, host, false, loginID)
		go listenWebSocketConn(host, ws, true, hostName)
	} else {
		log.Println("Adding player to registered clients: " + loginID)
		registeredClients.Store(loginID, ws)
	}
}

func privateMatchRequestHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	ws.SetCloseHandler(socketCloseHandler)
	player := playerStruct{
		ID:        "",
		MatchedID: "",
		IsHost:    false,
		Ws:        ws,
	}
	code := r.Header.Get("room-code")
	if code == "" {
		// this request is from a user who wants to host a private game
		codeGenerated := ""
		for {
			codeGenerated = randStringBytesMaskImprSrcSB(5)
			_, ok := privateClients.Load(codeGenerated)
			if !ok {
				break
			}
		}
		room := roomStruct{
			Code: codeGenerated,
		}
		player.Ws.WriteJSON(room)
		log.Println("Adding host to private client pool")
		player.IsHost = true
		privateClients.Store(codeGenerated, &player)
	} else {
		// this request is from a user who wants to join a private game
		var hostPlayer interface{}
		var loaded bool
		seconds := 10
		for {
			hostPlayer, loaded = privateClients.Load(code)
			if !loaded {
				log.Println("Could not find the host for this client in private game pool")
				time.Sleep(time.Second * 1)
				seconds--
				if seconds == 0 {
					ws.Close()
					return
				}
				continue
			}
			break
		}
		host, ok := hostPlayer.(*playerStruct)
		if !ok {
			// TODO: return error saying something is wrong
			log.Println("Error converting host to websocket connection")
		}
		unMatchedPlayer := player.match(host, atomic.AddUint64(&id, 1))
		if unMatchedPlayer == nil {
			log.Println("Successfully setup a private match!")
			privateClients.Delete(code)
		}
	}
}
