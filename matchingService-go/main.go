package main

import (
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

func (p playerStruct) match(mp *playerStruct, matchID uint64) *playerStruct {
	match := strconv.FormatUint(matchID, 10)
	mp.ID = match + "_host"
	p.ID = match + "_client"
	mp.MatchedID = p.ID
	p.MatchedID = mp.ID
	err := mp.Ws.WriteJSON(&mp)
	if err != nil {
		mp.Ws.Close()
		return &p
	}
	err = p.Ws.WriteJSON(p)
	if err != nil {
		p.Ws.Close()
		return mp
	}
	p.Ws.Close()
	mp.Ws.Close()
	return nil
}

var privateClients sync.Map
var registeredClients sync.Map
var publicClients = make(chan *playerStruct, 1000)
var upgrader = websocket.Upgrader{}
var id uint64

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
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
	timeout := time.Now().Add(time.Second * 60)
	conn.SetReadDeadline(timeout)
	sendConn.SetWriteDeadline(timeout)
	var message interface{}
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
	}
	log.Println("Timed-out/Done so closing connection for " + loginID)
	if isConnHost {
		conn.Close()
		sendConn.Close()
	}
}

func main() {
	router := mux.NewRouter()
	router.HandleFunc("/", healthCheckHandler).Methods("GET")
	router.HandleFunc("/publicMatch", publicMatchRequestHandler)
	router.HandleFunc("/privateMatch", privateMatchRequestHandler)
	router.HandleFunc("/rtcSetup", setupWebRTCConnHandler)

	log.Println("starting server at port: 8080")
	log.Fatal(http.ListenAndServe(":8080", router))
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "matching service running!")
}

func publicMatchRequestHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("publicMatch endpoint hit!")
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	player := playerStruct{
		ID:        "",
		MatchedID: "",
		IsHost:    false,
		Ws:        ws,
	}
	var matchedPlayer *playerStruct = nil
	select {
	case matchedPlayer = <-publicClients:
		log.Println("Popped player to channel")
		break
	case <-time.After(1 * time.Second):
		break
	}
	if matchedPlayer != nil {
		matchedPlayer.IsHost = true
		unMatchedPlayer := player.match(matchedPlayer, atomic.AddUint64(&id, 1))
		if unMatchedPlayer != nil {
			if unMatchedPlayer.IsHost {
				unMatchedPlayer.IsHost = false
			}
			publicClients <- unMatchedPlayer
		} else {
			log.Println("Successfully setup a public match!")
		}
	} else {
		log.Println("Pushing player to channel")
		publicClients <- &player
	}
}

func setupWebRTCConnHandler(w http.ResponseWriter, r *http.Request) {
	loginID := r.Header.Get("login-id")
	s := strings.Split(loginID, "_")
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	if s[1] == "client" {
		var hostPlayer interface{}
		var loaded bool
		seconds := 10
		for {
			hostPlayer, loaded = registeredClients.LoadAndDelete(s[0] + "_host")
			if !loaded {
				log.Println("Could not find the host for this client")
				time.Sleep(time.Second * 1)
				seconds--
				if seconds == 0 {
					break
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
		go listenWebSocketConn(host, ws, true, s[0]+"_host")
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
		log.Println("room code is: " + codeGenerated)
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
			hostPlayer, loaded = privateClients.LoadAndDelete(code)
			if !loaded {
				log.Println("Could not find the host for this client in private game pool")
				time.Sleep(time.Second * 1)
				seconds--
				if seconds == 0 {
					break
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
		if unMatchedPlayer != nil {
			if unMatchedPlayer.IsHost {
				privateClients.Store(code, unMatchedPlayer)
			}
		} else {
			log.Println("Successfully setup a private match!")
		}
	}
}
