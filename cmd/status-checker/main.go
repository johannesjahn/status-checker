package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"syscall"
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

func parseConfig(configPath string) {
	configBytes, err := os.ReadFile(configPath)
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

type args struct {
	configPath string
	staticPath string
	dataPath   string
	timeout    int
}

func parseArgs() args {
	var (
		configPath string
		staticPath string
		dataPath   string
		timeout    int
	)

	flag.StringVar(&configPath, "config", "./config.json", "path to the config file (default ./config.json)")
	flag.StringVar(&configPath, "c", "./config.json", "path to the config file (default ./config.json) (shorthand)")
	flag.StringVar(&staticPath, "static", "./static", "path to the static files (default ./static)")
	flag.StringVar(&staticPath, "s", "./static", "path to the static files (default ./static) (shorthand)")
	flag.IntVar(&timeout, "timeout", 10, "timeout in seconds (default 10)")
	flag.IntVar(&timeout, "t", 10, "timeout in seconds (default 10) (shorthand)")
	flag.StringVar(&dataPath, "data", "./data", "path to the data files (default ./data)")
	flag.StringVar(&dataPath, "d", "./data", "path to the data files (default ./data) (shorthand)")

	// Parse the flags
	flag.Parse()

	if flag.NArg() > 0 {
		fmt.Println("Positional arguments found")
		os.Exit(2)
	}

	if dataPath[len(dataPath)-1] != '/' {
		dataPath += "/"
	}

	fmt.Printf("Config Path: %s\n", configPath)
	fmt.Printf("Static Path: %s\n", staticPath)
	fmt.Printf("Data Path: %s\n", dataPath)
	fmt.Printf("Timeout: %d\n", timeout)

	return args{
		configPath: configPath,
		staticPath: staticPath,
		timeout:    timeout,
		dataPath:   dataPath,
	}
}

func checkConfigItem(item string) statusUpdate {
	timeStart := time.Now()
	resp, err := http.Get(item)
	if err != nil {
		log.Print("Error checking item: ", item, " Error: ", err.Error())
		stat := 0
		if !strings.Contains(err.Error(), "connect:") && !strings.Contains(err.Error(), "dial tcp:") && !strings.Contains(err.Error(), "timeout") {
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

func saveStatusState(views []StatusView, dataPath string) error {
	// saves the current state to a json file
	file, err := os.Create(dataPath + "status_state.json")
	if err != nil {
		log.Printf("Error creating file: %s", err)
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(views); err != nil {
		log.Printf("Error encoding JSON to file: %s", err)
		return err
	}
	return nil
}

func loadStatusState(dataPath string) ([]StatusView, error) {
	// loads the current state from a json file
	file, err := os.Open(dataPath + "status_state.json")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var statusViews []StatusView
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&statusViews); err != nil {
		return nil, err
	}

	// Convert the loaded status views back to the map format
	for _, statusView := range statusViews {
		statusState[statusView.Url] = StatusState{
			Healthy:       statusView.Healthy,
			LastHealthy:   time.Unix(statusView.LastHealth, 0),
			LastUnhealthy: time.Unix(statusView.LastUnhealthy, 0),
			ResponseCode:  statusView.ResponseCode,
			ResponseTime:  time.Duration(statusView.ResponseTime) * time.Millisecond,
		}
	}

	return statusViews, nil
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

	statusView := StatusStatesToView()
	err = conn.WriteJSON(statusView)
	if err != nil {
		log.Printf("Error writing to websocket: %s", err)
		delete(wsConnections, conn)
	}

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}

}

func main() {

	args := parseArgs()
	parseConfig(args.configPath)
	fmt.Println(config)

	http.Handle("/", http.FileServer(http.Dir(args.staticPath)))

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

	_, err := loadStatusState(args.dataPath)
	if err != nil {
		log.Printf("Error loading status state: %s", err)
	}

	for {
		updateStatusState()
		log.Print("Currently connected clients: ", len(wsConnections))
		statusView := StatusStatesToView()
		err := saveStatusState(statusView, args.dataPath)
		if err != nil {
			if errors.Is(err, syscall.ENOENT) {
				log.Printf("File not found while saving status state: %s", err)
				log.Printf("Creating directory: %s", args.dataPath)
				err := os.MkdirAll(args.dataPath, os.ModePerm)
				if err != nil {
					log.Printf("Error creating directory: %s", err)
				} else {
					log.Printf("Retrying to save status state")
					err = saveStatusState(statusView, args.dataPath)
				}
			} else {
				log.Printf("Error saving status state: %s", err)
			}
		}
		for conn := range wsConnections {
			err := conn.WriteJSON(statusView)
			if err != nil {
				log.Printf("Error writing to websocket: %s", err)
				delete(wsConnections, conn)
			}
		}
		time.Sleep(time.Duration(args.timeout) * time.Second)
	}

}
