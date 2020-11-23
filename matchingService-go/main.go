package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
)

type MatchingResult struct {
	MatchedIPAddress string `json:"matched_ip_address"`
	IsServer bool `json:"is_server"`
}

type MatchingRequest struct {
	IpAddress string `json:"ip_address"`
}

func sendMatchedInformation(player string, matchedPlayer string, isServer bool) (bool, error) {
	result := &MatchingResult{
		MatchedIPAddress: matchedPlayer,
		IsServer: isServer,
	}
	resultBinary, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest("POST", player, bytes.NewBuffer(resultBinary))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true, err
	}
	return false, err
}

func findMatch(playerIPAddresses <-chan string) {
	for {
		ipAddr1 := <-playerIPAddresses
		ipAddr2 := <-playerIPAddresses
		success, err := sendMatchedInformation(ipAddr1, ipAddr2, true)
		if !success {
			log.Println("Could not send request to Player with IP: " + ipAddr1)
			log.Println(err)
			// TODO: add player2 back to the pool
			continue
		}
		success, err = sendMatchedInformation(ipAddr2, ipAddr1, false)
		if !success {
			// TODO: add player1 back to the pool
			log.Println("Could not send request to Player with IP: " + ipAddr2)
			log.Println(err)
		}
	}	
}

func addPlayerHandler(w http.ResponseWriter, r *http.Request, playerIPAddresses chan<- string) {
	switch r.Method {
	case "POST":
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}
		log.Println(string(body))
		var m MatchingRequest
		err = json.Unmarshal(body, &m)
		if err != nil {
			panic(err)
		}
		log.Println(m)
		resp := map[string]string{
			"message": "Successfully added to pool",
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
		playerIPAddresses <- m.IpAddress
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "Only POST method supported for matching request.")
	}
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	resp := map[string]string{
		"message": "Service is running at 8080 port",
	}
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func main() {
	playerIPAddresses := make(chan string, 100)
	go findMatch(playerIPAddresses)
	// server setup
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthCheck(w, r)
	}))
	mux.Handle("/findMatch", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addPlayerHandler(w, r, playerIPAddresses)
	}))
	srv := &http.Server{
		Addr: ":8080",
		Handler: mux,
	}
	log.Println("starting server!")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen:%+s\n", err)
	}
}