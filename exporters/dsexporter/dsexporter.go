package main

import (
	"fmt"
	"net/http"
)

func ping(w http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(w, "pong")
}

func main() {
	http.HandleFunc("/ping", ping)

	fmt.Println("Starting dummy service on :8090")
	err := http.ListenAndServe(":8090", nil)
	if err != nil {
		fmt.Println("Error starting dummy service:", err)
	}
}
