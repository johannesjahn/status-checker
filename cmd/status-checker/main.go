package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type StatusState struct {
	Healthy       bool
	LastHealthy   time.Time
	LastUnhealthy time.Time
	ResponseCode  int
	ResponseTime  time.Duration
}

type StatusView struct {
	Url           string `json:"url"`
	Healthy       bool   `json:"healthy"`
	LastHealth    int64  `json:"lastHealthy"`
	LastUnhealthy int64  `json:"lastUnhealthy"`
	ResponseCode  int    `json:"responseCode"`
	ResponseTime  int64  `json:"responseTime"`
}

var config []string
var statusState map[string]StatusState = make(map[string]StatusState)

func parseConfig() {
	configBytes, err := os.ReadFile("../../config/default.json")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	err = json.Unmarshal(configBytes, &config)
	if err != nil {
		fmt.Println("Error parsing config:", err)
		return
	}

	fmt.Printf("Parsed Config: %+v\n", config)

	for _, item := range config {
		statusState[item] = StatusState{Healthy: true}
	}
}

func checkConfigItem(item string) statusUpdate {
	timeStart := time.Now()
	resp, err := http.Get(item)
	if err != nil {
		log.Print("Error checking item: ", item, " Error: ", err.Error())
		stat := 0
		if !strings.Contains(err.Error(), "connect: connection refused") && !strings.Contains(err.Error(), "connect: operation timed out") {
			stat = resp.StatusCode
		}

		return statusUpdate{item, StatusState{
			Healthy:       false,
			ResponseTime:  time.Since(timeStart),
			ResponseCode:  stat, // Set to 0 as there is no response code
			LastHealthy:   statusState[item].LastHealthy,
			LastUnhealthy: time.Now()}}
	}

	healthy := resp.StatusCode >= 200 && resp.StatusCode < 300

	return statusUpdate{item, StatusState{
		Healthy:       healthy,
		ResponseTime:  time.Since(timeStart),
		ResponseCode:  resp.StatusCode,
		LastHealthy:   time.Now(),
		LastUnhealthy: statusState[item].LastUnhealthy}}
}

type statusUpdate struct {
	item  string
	state StatusState
}

func updateStatusState() {
	updateChannel := make(chan statusUpdate)

	for _, item := range config {
		go func(item string) {
			result := checkConfigItem(item)
			updateChannel <- result
		}(item)
	}
	numberOfStatusUpdatesReceived := 0
	for update := range updateChannel {
		statusState[update.item] = update.state
		numberOfStatusUpdatesReceived++
		if numberOfStatusUpdatesReceived == len(config) {
			close(updateChannel)
		}
	}
}

func StatusStatesToView() []StatusView {
	var statusViews []StatusView
	for item, state := range statusState {
		statusViews = append(statusViews, state.toStatusView(item))
	}
	sort.Slice(statusViews, func(i, j int) bool {
		return statusViews[i].Url < statusViews[j].Url
	})
	return statusViews
}

func (s StatusState) toStatusView(item string) StatusView {
	return StatusView{
		Url:           item,
		Healthy:       s.Healthy,
		LastHealth:    s.LastHealthy.Unix(),
		LastUnhealthy: s.LastUnhealthy.Unix(),
		ResponseCode:  s.ResponseCode,
		ResponseTime:  s.ResponseTime.Milliseconds(),
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins (for development)
	},
}

var wsConnections = make(map[*websocket.Conn]interface{})

func handleConnections(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	wsConnections[conn] = nil

	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("Error closing connection: %s", err)
		}
		delete(wsConnections, conn)
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}

}

func main() {

	parseConfig()
	fmt.Println(config)

	http.Handle("/", http.FileServer(http.Dir("../../static")))

	http.HandleFunc("/status-json", func(w http.ResponseWriter, r *http.Request) {
		statusViews := StatusStatesToView()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statusViews)
	})

	http.HandleFunc("/ws", handleConnections)

	go func() {
		fmt.Println("Starting server at :8081")
		if err := http.ListenAndServe(":8081", nil); err != nil {
			fmt.Println("Error starting server:", err)
		}
	}()

	for {
		updateStatusState()
		log.Print("Currently connected clients: ", len(wsConnections))
		statusView := StatusStatesToView()
		for conn := range wsConnections {
			err := conn.WriteJSON(statusView)
			if err != nil {
				log.Printf("Error writing to websocket: %s", err)
				delete(wsConnections, conn)
			}
		}
		time.Sleep(5000 * time.Millisecond)
	}

}
