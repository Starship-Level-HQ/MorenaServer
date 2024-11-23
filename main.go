package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const bufferSize = 1024 * 16

type Enemy struct {
	Id         int     `json:"id"`
	X          float32 `json:"x"`
	Y          float32 `json:"y"`
	Xv         float32 `json:"xv"`
	Yv         float32 `json:"yv"`
	DirectionX string  `json:"directionX"`
	DirectionY string  `json:"directionY"`
	Health     int     `json:"health"`
}

type Player struct {
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
	host    bool
	Port    string `json:"port"`
	Alive   bool   `json:"alive"`
	*Player
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
	channels           = make(map[string]map[string]*Client)
	routinesPerChannel = make(map[string]bool)
	enemiesPerChannel  = make(map[string][]*Enemy)
	mutex              sync.Mutex
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
			Player: &Player{
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
			fmt.Println("Client disconnected:", client.id)
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

			if !routinesPerChannel[channelName] {
				routinesPerChannel[channelName] = true
				client.host = true
				go enemyRoutine(channelName, client)
			}
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
			for _, otherClient := range channels[client.channel] {
				if otherClient == client {
					continue
				}
				if otherClient.Alive {
					jsonOtherClient, err := json.Marshal(otherClient)
					if err != nil {
						fmt.Println("Error encoding JSON:", err)
						return
					}
					fmt.Println("JSON CLIENT: ", string(jsonOtherClient))
					client.conn.Write([]byte("__JSON__START__" + string(jsonOtherClient) + "__JSON__END__"))
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

			var newState Player
			err := json.Unmarshal([]byte(jsonMessage), &newState)
			if err != nil {
				fmt.Println("Error decoding JSON:", err)
				return
			}

			client.Player = &Player{
				X:          newState.X,
				Y:          newState.Y,
				Xv:         newState.Xv,
				Yv:         newState.Yv,
				DirectionX: newState.DirectionX,
				DirectionY: newState.DirectionY,
				Health:     newState.Health,
			}

			jsonClient, err := json.Marshal(client)
			if err != nil {
				fmt.Println("Error encoding JSON:", err)
				return
			}
			broadcastMessage(string(jsonClient), client)
		}
	} else if strings.Contains(message, "__JSON__ENEMY__START__") && client.host {
		start := strings.Index(message, "__JSON__ENEMY__START__") + len("__JSON__ENEMY__START__")
		end := strings.Index(message, "__JSON__ENEMY__END__")
		if start != -1 && end != -1 && end > start {
			jsonMessage := message[start:end]

			var enemies []Enemy
			err := json.Unmarshal([]byte(jsonMessage), &enemies)
			if err != nil {
				fmt.Println("Error decoding enemies JSON:", err)
				return
			}

			mutex.Lock()
			// Обновляем врагов
			for _, newEnemy := range enemies {
				for i, existingEnemy := range enemiesPerChannel[client.channel] {
					if existingEnemy.Id == newEnemy.Id {
						// Обновляем параметры врага
						existingEnemy.X = newEnemy.X
						existingEnemy.Y = newEnemy.Y
						existingEnemy.Xv = newEnemy.Xv
						existingEnemy.Yv = newEnemy.Yv
						existingEnemy.DirectionX = newEnemy.DirectionX
						existingEnemy.DirectionY = newEnemy.DirectionY
						existingEnemy.Health = newEnemy.Health

						// Если здоровье меньше или равно 0, удаляем врага из списка
						if existingEnemy.Health <= 0 {
							// Удаляем врага из среза
							enemiesPerChannel[client.channel] = append(
								enemiesPerChannel[client.channel][:i],
								enemiesPerChannel[client.channel][i+1:]...,
							)
						}
						break
					}
				}
			}
			mutex.Unlock()

			jsonEnemies, err := json.Marshal(enemiesPerChannel[client.channel])
			if err != nil {
				fmt.Println("Error encoding enemies JSON:", err)
				return
			}

			// передаем всем игрокам новое состояние врагов
			mutex.Lock()
			for _, anotherClient := range channels[client.channel] {
				anotherClient.conn.Write([]byte("__JSON__ENEMY__START__" + string(jsonEnemies) + "__JSON__ENEMY__END__"))
			}
			mutex.Unlock()
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

func enemyRoutine(channelName string, hostClient *Client) {
	for hostClient.Alive {
		mutex.Lock()

		enemies := enemiesPerChannel[channelName]

		// создаем врагов
		if len(enemies) < 5 {
			newEnemy := generateEnemy()
			enemiesPerChannel[channelName] = append(enemies, newEnemy)
			fmt.Printf("Создан новый враг в канале %s: %+v\n", channelName, newEnemy)

			enemyData, err := json.Marshal(newEnemy)
			if err != nil {
				fmt.Println("Error encoding enemy JSON:", err)
			}
			broadcastMessage("__ADDENEMY__START__"+string(enemyData)+"__ADDENEMY__END__", hostClient)
		}

		mutex.Unlock()

		time.Sleep(5 * time.Second)
	}

	// останавливаем горутину
	mutex.Lock()
	routinesPerChannel[channelName] = false
	hostClient.host = false
	mutex.Unlock()
}

func generateEnemy() *Enemy {
	return &Enemy{
		Id:         rand.Intn(1000),
		X:          float32(rand.Intn(500)),
		Y:          float32(rand.Intn(500)),
		Xv:         0,
		Yv:         0,
		DirectionX: "",
		DirectionY: "",
		Health:     100,
	}
}
