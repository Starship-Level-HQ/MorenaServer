package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
)

const bufferSize = 1024 * 16

type PlayerState struct {
	X          float32 `json:"x"`
	Y          float32 `json:"y"`
	Xv         float32 `json:"xv"`
	Yv         float32 `json:"yv"`
	DirectionX string  `json:"directionX"`
	DirectionY string  `json:"directionY"`
	Health     int     `json:"health"`
}

type Client struct {
	conn    net.Conn
	channel string
	id      string
	buffer  []byte
	Port    string `json:"port"`
	Alive   bool   `json:"alive"`
	*PlayerState
}

var (
	cfg = struct {
		port            string
		sendOwnMessages bool
		verbose         bool
	}{
		port:            "1337",
		sendOwnMessages: true,
		verbose:         true,
	}
	channels = make(map[string]map[string]*Client)
	mutex    sync.Mutex
)

func main() {
	listener, err := net.Listen("tcp", ":"+cfg.port)
	if err != nil {
		fmt.Println("Error starting server:", err)
		return
	}
	defer listener.Close()
	fmt.Println("Server started on port:", cfg.port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}

		client := &Client{
			conn:   conn,
			id:     conn.RemoteAddr().String(),
			buffer: make([]byte, bufferSize),
			Port:   strconv.Itoa(conn.RemoteAddr().(*net.TCPAddr).Port),
			Alive:  true,
			PlayerState: &PlayerState{
				X:          300,
				Y:          300,
				Xv:         300,
				Yv:         300,
				DirectionX: "",
				DirectionY: "",
				Health:     100,
			},
		}
		go handleConnection(client)
	}
}

func handleConnection(client *Client) {
	defer client.conn.Close()

	for client.Alive {
		n, err := client.conn.Read(client.buffer) // некоторые данные могут остаться в буфере TCP соединения, если не поместятся
		if err != nil {
			fmt.Println("Client disconnected:", client.id) // TODO очистить буфер после выхода
			client.Alive = false

			// определяем начальное состояние
			jsonClient, err := json.Marshal(client)
			if err != nil {
				fmt.Println("Error encoding JSON:", err)
				return
			}

			// говорим всем что игрок отключился
			broadcastMessage(string(jsonClient), client)

			break
		}

		processMessage(string(client.buffer[:n]), client)
	}
}

func processMessage(message string, client *Client) {

	if strings.Contains(message, "__SUBSCRIBE__") {

		start := strings.Index(message, "__SUBSCRIBE__") + len("__SUBSCRIBE__")
		end := strings.Index(message, "__ENDSUBSCRIBE__")

		if start >= 0 && end > start {
			channelName := message[start:end]
			client.channel = channelName

			mutex.Lock()
			if channels[channelName] == nil {
				channels[channelName] = make(map[string]*Client)
			}
			channels[channelName][client.id] = client
			mutex.Unlock()

			// определяем начальное состояние
			jsonClient, err := json.Marshal(client)
			if err != nil {
				fmt.Println("Error encoding JSON:", err)
				return
			}

			// состояние нового игрока отправляется всем остальным
			broadcastMessage(string(jsonClient), client)

			// состояние всех остальных игроков отправляется новому
			mutex.Lock()
			for _, currClient := range channels[client.channel] {
				if currClient == client {
					continue
				}
				if currClient.Alive {
					jsonCurrClient, err := json.Marshal(currClient)
					if err != nil {
						fmt.Println("Error encoding JSON:", err)
						return
					}
					fmt.Println("JSON CLIENT: ", string(jsonCurrClient))
					client.conn.Write([]byte("__JSON__START__" + string(jsonCurrClient) + "__JSON__END__"))
				}
			}
			mutex.Unlock()

			fmt.Println("Client", client.id, "subscribed to", channelName)
		} else {
			fmt.Println("Error: __SUBSCRIBE__ block malformed")
		}
	} else if strings.Contains(message, "__JSON__START__") {

		start := strings.Index(message, "__JSON__START__") + len("__JSON__START__")
		end := strings.Index(message, "__JSON__END__")
		if start != -1 && end != -1 && end > start {
			jsonMessage := message[start:end]

			var state PlayerState
			err := json.Unmarshal([]byte(jsonMessage), &state)
			if err != nil {
				fmt.Println("Error decoding JSON:", err)
				return
			}

			client.PlayerState = &PlayerState{
				X:          state.X,
				Y:          state.Y,
				Xv:         state.Xv,
				Yv:         state.Yv,
				DirectionX: state.DirectionX,
				DirectionY: state.DirectionY,
				Health:     state.Health,
			}
			// fmt.Println(client.GameState)
			jsonClient, err := json.Marshal(client)
			if err != nil {
				fmt.Println("Error encoding JSON:", err)
				return
			}
			broadcastMessage(string(jsonClient), client)
		}
	}
}

func broadcastMessage(jsonMessage string, sender *Client) {
	mutex.Lock()
	defer mutex.Unlock()

	for _, client := range channels[sender.channel] {
		if !cfg.sendOwnMessages && client == sender {
			continue
		}
		client.conn.Write([]byte("__JSON__START__" + string(jsonMessage) + "__JSON__END__"))
	}
}
