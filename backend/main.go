package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

type response struct {
	Message string `json:"message"`
	Host    string `json:"host"`
	Time    string `json:"time"`
}

func hello(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(response{
		Message: "Привет от бэкенда на Go!",
		Host:    host,
		Time:    time.Now().Format(time.RFC3339),
	})
}

func main() {
	http.HandleFunc("/api/hello", hello)
	log.Println("backend listening on :5000")
	log.Fatal(http.ListenAndServe(":5000", nil))
}
