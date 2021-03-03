package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

type matchResponse struct {
	ID        string `json:"id"`
	MatchedID string `json:"matched_id"`
	IsHost    bool   `json:"is_host"`
}

type signalMessage struct {
	Text string `json:"text"`
}

type roomStruct struct {
	Code string `json:"room_code"`
}

func socketCloseHandler(code int, text string) error {
	log.Println("WebSocket closed with code: {} and message: "+text, code)
	return &websocket.CloseError{
		Code: code,
		Text: text,
	}
}

func main() {
	argsWithProg := os.Args
	serverURL := "ws://localhost:8080"
	// req, err := http.NewRequest("CONNECT", serverURL+"/publicMatch", nil)
	req, err := http.NewRequest("CONNECT", serverURL+"/privateMatch", nil)
	if err != nil {
		log.Fatal(err)
	}
	if len(argsWithProg) > 1 {
		req.Header.Add("room-code", argsWithProg[1])
	}
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 45 * time.Second,
	}
	// clientConn, resp, err := dialer.Dial(serverURL+"/publicMatch", req.Header)
	clientConn, resp, err := dialer.Dial(serverURL+"/privateMatch", req.Header)
	if err != nil {
		log.Fatal(err)
	}
	clientConn.SetCloseHandler(socketCloseHandler)
	log.Println("Resp code is: " + resp.Status)
	var message interface{}
	matchResp := matchResponse{}
	// find partner to play
	for {
		if err = clientConn.ReadJSON(&message); err != nil {
			log.Println(err)
			clientConn.Close()
			break
		}
		log.Println(message)
		msgMap, msgErr := message.(map[string]interface{})
		if !msgErr {
			log.Println("Could not conver message to map")
			break
		}
		b, err := json.Marshal(msgMap)
		if err != nil {
			panic(err)
		}
		json.Unmarshal(b, &matchResp)
		log.Println(matchResp)
	}
	// setup connection to this partner
	req, err = http.NewRequest("CONNECT", serverURL+"/rtcSetup", nil)
	req.Header.Add("login-id", matchResp.ID)
	rtcConn, _, err := dialer.Dial(serverURL+"/rtcSetup", req.Header)
	if err != nil {
		log.Fatal(err)
	}
	rtcConn.SetCloseHandler(socketCloseHandler)
	signalResp := signalMessage{}
	if matchResp.IsHost {
		for {
			if err = rtcConn.ReadJSON(&message); err != nil {
				log.Println(err)
				rtcConn.Close()
				break
			}
			msgMap, msgErr := message.(map[string]interface{})
			if !msgErr {
				log.Println("Could not conver message to map")
				break
			}
			b, err := json.Marshal(msgMap)
			if err != nil {
				panic(err)
			}
			json.Unmarshal(b, &signalResp)
			log.Println(signalResp)
			if signalResp.Text == "Your Turn" {
				signalResp.Text = "Hi I am server"
				rtcConn.WriteJSON(signalResp)
				signalResp.Text = "Bye"
				rtcConn.WriteJSON(signalResp)
				rtcConn.Close()
				break
			}
		}
	} else {
		signalResp.Text = "Hi this is client"
		rtcConn.WriteJSON(signalResp)
		signalResp.Text = "I am talking to you"
		rtcConn.WriteJSON(signalResp)
		signalResp.Text = "Your Turn"
		rtcConn.WriteJSON(signalResp)
		for {
			if err = rtcConn.ReadJSON(&message); err != nil {
				log.Println(err)
				rtcConn.Close()
				break
			}
			msgMap, msgErr := message.(map[string]interface{})
			if !msgErr {
				log.Println("Could not conver message to map")
				break
			}
			b, err := json.Marshal(msgMap)
			if err != nil {
				panic(err)
			}
			json.Unmarshal(b, &signalResp)
			log.Println(signalResp)
			if signalResp.Text == "Bye" {
				break
			}
		}
	}
	rtcConn.Close()
}
