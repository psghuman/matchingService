package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
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

type registerUserResponseMessage struct {
	ID string `json:"id"`
}

var players sync.Map
var privateMatchRooms sync.Map
var registeredClients sync.Map

var publicClients = make(chan string, 1000)
var upgrader = websocket.Upgrader{}

const letterBytes = "ABCDEFGHJKLMNOPQRSTUVWXYZ0123456789"
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

var numPlayersCreated uint64
var numPublicMatches uint64
var numPrivateMatches uint64
var version string

func main() {
	router := mux.NewRouter()
	router.HandleFunc("/", healthCheckHandler).Methods("GET")
	router.HandleFunc("/checkRoomCode", checkRoomCodeHandler).Methods("GET")
	router.HandleFunc("/createUser", registerUserHandler).Methods("GET")
	router.HandleFunc("/closeSocket", playerSocketCloseHandler).Methods("GET")
	router.HandleFunc("/deleteUser", deleteUserHandler).Methods("GET")
	router.HandleFunc("/stats", getStatistics).Methods("GET")
	router.HandleFunc("/version", getVersion).Methods("GET")
	router.HandleFunc("/publicMatch", publicMatchRequestHandler)
	router.HandleFunc("/privateMatch", privateMatchRequestHandler)
	router.HandleFunc("/rtcSetup", setupWebRTCConnHandler)

	log.Println("starting server at port: 8080")
	go setupPublicMatches()
	numPlayersCreated = 0
	numPublicMatches = 0
	numPrivateMatches = 0
	version = "1.2.0"
	log.Fatal(http.ListenAndServe(":8080", router))
}

func (p playerStruct) match(mp *playerStruct) *playerStruct {
	if p.Ws == nil {
		log.Println("found ws while matching to be nil for " + p.ID)
		return mp
	}
	if mp.Ws == nil {
		log.Println("found ws while matching to be nil for " + mp.ID)
		return &p
	}
	mp.MatchedID = p.ID
	p.MatchedID = mp.ID
	err1 := p.Ws.WriteJSON(p)
	if err1 != nil {
		p.Ws.Close()
		p.Ws = nil
		return mp
	}
	err2 := mp.Ws.WriteJSON(&mp)
	if err2 != nil {
		mp.Ws.Close()
		mp.Ws = nil
		return &p
	}
	writeWait := time.Duration(30 * time.Second)
	p.Ws.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(writeWait))
	p.Ws.Close()
	p.Ws = nil
	mp.Ws.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(writeWait))
	mp.Ws.Close()
	mp.Ws = nil
	return nil
}

func LoadPlayer(id string) *playerStruct {
	playerPt, loaded := players.Load(id)
	if !loaded {
		log.Println("Could not load player with id: " + id)
		return nil
	}
	player, ok := playerPt.(*playerStruct)
	if !ok {
		log.Println("player stored incorrectly for idL " + id)
		return nil
	}
	return player
}

func setupPublicMatches() {
	var client *playerStruct = nil
	var host *playerStruct = nil
	clientID := ""
	hostID := ""
	for {
		if clientID == "" {
			clientID = <-publicClients
		}
		if hostID == "" {
			hostID = <-publicClients
		}
		if clientID == hostID {
			log.Println("found same player while matching! Resetting")
			hostID = ""
			continue
		}
		client = LoadPlayer(clientID)
		if client == nil {
			// TODO: maybe return error saying something is wrong
			log.Println("player no longer exists, finding another match")
			clientID = ""
			continue
		}
		host = LoadPlayer(hostID)
		if host == nil {
			// TODO: maybe return error saying something is wrong
			log.Println("player no longer exists, finding another match")
			hostID = ""
			continue
		}
		host.IsHost = true
		unMatchedPlayer := client.match(host)
		if unMatchedPlayer == nil {
			log.Println("Successfully setup a public match!")
			clientID = ""
			hostID = ""
			go updatePublicMatchesCount()
		} else {
			if unMatchedPlayer.IsHost {
				host = unMatchedPlayer
				client = nil
				clientID = ""
			} else {
				host = nil
				hostID = ""
				client = unMatchedPlayer
			}
		}
	}
}

func listenWebSocketConn(conn *websocket.Conn, sendConn *websocket.Conn, isConnHost bool, loginID string, ch chan<- bool) {
	timeout := time.Time{}
	conn.SetReadDeadline(timeout)
	sendConn.SetWriteDeadline(timeout)
	var message interface{}
	messagesSent := 0
	ch <- true
	for {
		err := conn.ReadJSON(&message)
		if err != nil {
			log.Println(err)
			break
		}
		err = sendConn.WriteJSON(&message)
		if err != nil {
			log.Println(err)
			break
		}
		log.Println(loginID + ": forwarded a message")
		messagesSent++
	}
	log.Println("Timed-out/Done so closing connection for " + loginID)
	if !isConnHost {
		writeWait := time.Duration(30 * time.Second)
		conn.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(writeWait))
		conn.Close()
		sendConn.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(writeWait))
		sendConn.Close()
	} else {
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

func handleWebSocketClose(c *websocket.Conn) {
	if c == nil {
		return
	}
	writeWait := time.Duration(30 * time.Second)
	for {
		if _, _, err := c.NextReader(); err != nil {
			c.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(writeWait))
			c.Close()
			break
		}
	}
}

func updatePlayerCount() {
	atomic.AddUint64(&numPlayersCreated, 1)
}

func updatePublicMatchesCount() {
	atomic.AddUint64(&numPublicMatches, 1)
}

func updatePrivateMatchesCount() {
	atomic.AddUint64(&numPrivateMatches, 1)
}

func getStatistics(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Players: %d\nPublic: %d\nPrivate: %d\n", numPlayersCreated, numPublicMatches, numPrivateMatches)
}

func getVersion(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Version: "+version)
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "matching service running!")
}

func checkRoomCodeHandler(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code != "" {
		_, ok := privateMatchRooms.Load(code)
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

func playerSocketCloseHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	code := r.URL.Query().Get("room")
	if id != "" {
		player := LoadPlayer(id)
		if player == nil {
			log.Println("player id does not exist")
			http.Error(w, "id not correct", http.StatusBadRequest)
			return
		}
		go handleWebSocketClose(player.Ws)
		player.Ws = nil
		log.Println("set ws for player id: " + id + " to nil")
		_, loaded := privateMatchRooms.Load(code)
		if loaded {
			privateMatchRooms.Delete(code)
			log.Println("removed room with code: " + code)
		}
		fmt.Fprintf(w, "closed websocket!")
		return
	}
	http.Error(w, "id not sent", http.StatusBadRequest)
}

func registerUserHandler(w http.ResponseWriter, r *http.Request) {
	id := ""
	queryVersion := r.URL.Query().Get("v")
	if queryVersion == "" {
		http.Error(w, "Error! version not sent!", http.StatusBadRequest)
		return
	}
	s1 := strings.Split(version, ".")
	s2 := strings.Split(queryVersion, ".")
	if len(s1) != len(s2) {
		http.Error(w, "Error! incorrect version formatting!", http.StatusBadRequest)
		return
	}
	if s1[0] != s2[0] || s1[1] != s2[1] {
		http.Error(w, "Error! older version sent!", http.StatusBadRequest)
		return
	}
	for {
		id = randStringBytesMaskImprSrcSB(8)
		id = "user_" + id
		_, ok := players.Load(id)
		if !ok {
			player := playerStruct{
				ID:        id,
				MatchedID: "",
				IsHost:    false,
				Ws:        nil,
			}
			players.Store(id, &player)
			log.Println("created user with id {}", id)
			resp := registerUserResponseMessage{
				ID: id,
			}
			b, err := json.Marshal(resp)
			if err != nil {
				http.Error(w, "something went wrong", http.StatusBadRequest)
				return
			}
			w.Write(b)
			log.Println("sent message! ending handler call")
			go updatePlayerCount()
			return
		}
	}
}

func deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	code := r.URL.Query().Get("room")
	if id != "" {
		players.Delete(id)
		if code != "" {
			privateMatchRooms.Delete(code)
			log.Println("removed room with code: " + code)
		}
		fmt.Fprintf(w, "deleted!")
		log.Println("deleted user with id {}", id)
		return
	}
	http.Error(w, "id not sent", http.StatusBadRequest)
}

func publicMatchRequestHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("id")
	if userID != "" {
		player := LoadPlayer(userID)
		if player != nil {
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				log.Fatal(err)
			}
			ws.SetCloseHandler(socketCloseHandler)
			player.Ws = ws
			player.IsHost = false
			player.MatchedID = ""
			log.Println("Pushing player to channel")
			publicClients <- userID
		}
	}
}

func setupWebRTCConnHandler(w http.ResponseWriter, r *http.Request) {
	loginID := r.Header.Get("login-id")
	matchedUserID := r.Header.Get("matched-user-id")
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	ws.SetCloseHandler(socketCloseHandler)
	player := LoadPlayer(loginID)
	if player == nil {
		return
	}
	if !player.IsHost {
		var hostPlayer interface{}
		var loaded bool
		seconds := 10
		hostID := matchedUserID
		for {
			hostPlayer, loaded = registeredClients.Load(hostID)
			if !loaded {
				log.Println("Could not find the host: " + hostID)
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
		var syncConn = make(chan bool, 2)
		go listenWebSocketConn(ws, host, false, loginID, syncConn)
		go listenWebSocketConn(host, ws, true, hostID, syncConn)
		_ = <-syncConn
		_ = <-syncConn
	} else {
		log.Println("Adding player to registered clients: " + loginID)
		registeredClients.Store(loginID, ws)
	}
}

func privateMatchRequestHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("id")
	player := LoadPlayer(userID)
	if player == nil {
		log.Println("could not find player in private endpoint")
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	ws.SetCloseHandler(socketCloseHandler)
	player.Ws = ws
	player.IsHost = false
	player.MatchedID = ""
	code := r.Header.Get("room-code")
	if code == "" {
		// this request is from a user who wants to host a private game
		codeGenerated := ""
		for {
			codeGenerated = randStringBytesMaskImprSrcSB(5)
			_, ok := privateMatchRooms.Load(codeGenerated)
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
		privateMatchRooms.Store(codeGenerated, userID)
	} else {
		// this request is from a user who wants to join a private game
		var hostIDInterface interface{}
		var loaded bool
		seconds := 10
		for {
			hostIDInterface, loaded = privateMatchRooms.Load(code)
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
		hostID, _ := hostIDInterface.(string)
		host := LoadPlayer(hostID)
		if host != nil {
			unMatchedPlayer := player.match(host)
			if unMatchedPlayer == nil {
				log.Println("Successfully setup a private match!")
				go updatePrivateMatchesCount()
			} else {
				log.Println("Error! could not setup a private match!")
			}
		}
	}
}
