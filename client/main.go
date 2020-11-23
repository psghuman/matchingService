package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
)

type MatchingResult struct {
	MatchedIPAddress string `json:"matched_ip_address"`
	IsServer bool `json:"is_server"`
}

type MatchingRequest struct {
	IpAddress string `json:"ip_address"`
}

func matchedPlayerHandler(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		panic(err)
	}
	var m MatchingResult
	err = json.Unmarshal(body, &m)
	if err != nil {
		panic(err)
	}
	log.Println(m)
	w.WriteHeader(http.StatusOK)
}

func main()  {
	port := os.Args[1]
	serviceHost := os.Args[2]
	http.HandleFunc("/", matchedPlayerHandler)

	ipAddress := "http://localhost:" + port
	payload := &MatchingRequest{IpAddress: ipAddress}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("POST", serviceHost, bytes.NewBuffer(payloadBytes))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		if err := http.ListenAndServe(":" + port, nil); err != nil {
			panic(err)
		}
	}
}