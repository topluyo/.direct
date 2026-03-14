package main

import (
	"database/sql"
	//"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"strings"
	"strconv"
	"sync"
	"time"
	"os"
	"encoding/json"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"
)

type Online struct {
	GroupID int
	ChannelID int
	UserID int
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	online = make(map[*websocket.Conn]Online)
	onlineMu sync.RWMutex
	db            *sql.DB
)



func setConn(ws *websocket.Conn, user Online) {
	onlineMu.Lock()           // yazma kilidi alındı
	online[ws] = user
	onlineMu.Unlock()         // yazma kilidi bırakıldı
}

func delConn(ws *websocket.Conn) {
	onlineMu.Lock()
	delete(online, ws)
	onlineMu.Unlock()
}

func getConn(ws *websocket.Conn) (Online, bool) {
	onlineMu.RLock()          // okuma kilidi alındı
	user, ok := online[ws]
	onlineMu.RUnlock()        // okuma kilidi bırakıldı
	return user, ok
}

func broadcast( group_id int, channel_id int, user_id int, msg string) {
	onlineMu.RLock()
	defer onlineMu.RUnlock()
	for ws,o := range online {
		if((user_id==0 || o.UserID==user_id) && (group_id==0 || o.GroupID==group_id) && (channel_id==0 || o.ChannelID==channel_id)){
			ws.WriteMessage(websocket.TextMessage, []byte(msg))
		}
	}
}

func users (group_id int, channel_id int, user_id int) []websocket.Conn{
	onlineMu.RLock()
	defer onlineMu.RUnlock()
	response := make([]websocket.Conn, 0)
	for ws,o := range online {
		if((user_id==0 || o.UserID==user_id) && (group_id==0 || o.GroupID==group_id) && (channel_id==0 || o.ChannelID==channel_id)){
			response = append(response,*ws)
		}
	}
	return response
}

func ToInt(number string) int {
	i, err := strconv.Atoi(number)
	if err != nil {
		return 0
	}
	return i
}

func ToString(number int) string {
	return strconv.Itoa(number)
}


func ToJson(data interface{}) string {
	bytes, err := json.Marshal(data)
	if err != nil {
		return "{}" 
	}
	return string(bytes)
}



func main() {
	var err error
	db, err = sql.Open("mysql", "master:master@tcp(127.0.0.1:3306)/db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	http.HandleFunc("/!direct-signal", func(w http.ResponseWriter, r *http.Request){
		query      := r.URL.Query()
    part       := query.Get("part")
		parts      := strings.Split(part, ",")
		group_id   := ToInt(parts[0])
		channel_id := ToInt(parts[1])
		user_id    := ToInt(parts[2])
		message    := query.Get("message")
		broadcast(group_id,channel_id,user_id,message)
	})
	http.HandleFunc("/!direct", onlineHandler)
	port := argument("port")
	log.Println("Server started at :"+port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func onlineHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer ws.Close()
	defer delConn(ws)

	// Set read deadlines and pong handler
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Ping ticker to keep connection alive
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}()

	var userID int
	for {
		_, message, err := ws.ReadMessage()
		if err != nil { break }
		userID = Func_User_ID(r, string(message))
		break
	}



	var o Online = Online{
		GroupID: 0,
		ChannelID: 0,
		UserID: userID,
	}
	setConn(ws,o)

	for{
		_, message, err := ws.ReadMessage()
		if err != nil { break }

		request := string(message)
		if (strings.HasPrefix(request, ":")) {
			// Sending Broadcast Message All User
			conn, ok := getConn(ws)
			if(ok){
				broadcast(conn.GroupID,conn.ChannelID,0,request[1:])
			}
		}else{
			// Setting group
			parts := strings.Split(request, ",")			
			o = Online{
				GroupID: ToInt(parts[0]),
				ChannelID: ToInt(parts[1]),
				UserID: userID,
			}
			setConn(ws,o)
		}
	}
}



func Func_User_IP_Address(r *http.Request) string {
	headers := []string{"CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP"}
	for _, h := range headers {
		ips := r.Header.Get(h)
		if ips != "" {
			return strings.TrimSpace(strings.Split(ips, ",")[0])
		}
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func Func_User_New_Negative_Hash_Number(s string) int {
	h := fnv.New32a()
	h.Write([]byte(s))
	return -int(h.Sum32() & 0x7FFFFFFF)
}

func Func_User_ID(r *http.Request, token string) int {
	var user_id int
	if token != "" {
		db.QueryRow("SELECT `user_id` FROM `session` WHERE `token` = ? AND `expire` > UNIX_TIMESTAMP()", token).Scan(&user_id)
	}
	if user_id == 0 {
		user_id = Func_User_New_Negative_Hash_Number(Func_User_IP_Address(r))
	}
	return user_id
}



type Struct_User struct {
  ID                        int         `db:"id" json:"id"`   
  Name                      string      `db:"name" json:"name"` 
  Nick                      string      `db:"nick" json:"nick"`
  Image                     string      `db:"image" json:"image"`
}


var user_query string = "SELECT id,name,nick,image FROM `user` WHERE id=? AND blocked=0"
func Func_User_Info(id int) *Struct_User  {
	var s Struct_User
	db.QueryRow(user_query, id).Scan(
    &s.ID,
    &s.Name,
    &s.Nick,
    &s.Image,
  )
	return &s
}




func argument(a string) string{
	response := ""
	for _,arg := range os.Args{
		if(strings.HasPrefix(arg, a+"=") ){
			response=strings.Split(arg, "=")[1]
			response=strings.Trim(response, "\"")
		}
	}
	return response
}
