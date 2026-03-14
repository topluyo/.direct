package main

import (
	"database/sql"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"strings"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"os"
	"encoding/json"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-memdb"
)

// Connection — go-memdb'de saklanan bağlantı kaydı
type Connection struct {
	ID           string          // ws pointer adresi ("%p")
	WS           *websocket.Conn
	WriteMu      sync.Mutex      // ws yazma kilidi (concurrent write koruması)
	UserID       int
	GroupID      int
	ChannelID    int
	ChannelGroup string          // "channelID,groupID" compound index
}

// safeWrite — concurrent write panic'i önlemek için mutex ile yazma
func (c *Connection) safeWrite(messageType int, data []byte) error {
	c.WriteMu.Lock()
	defer c.WriteMu.Unlock()
	return c.WS.WriteMessage(messageType, data)
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	mdb           *memdb.MemDB
	db            *sql.DB
	testMode      bool
	testUserSeq   atomic.Int64
)

// go-memdb schema tanımı
func createSchema() *memdb.DBSchema {
	return &memdb.DBSchema{
		Tables: map[string]*memdb.TableSchema{
			"connections": {
				Name: "connections",
				Indexes: map[string]*memdb.IndexSchema{
					// Primary index — ws pointer adresi
					"id": {
						Name:    "id",
						Unique:  true,
						Indexer: &memdb.StringFieldIndex{Field: "ID"},
					},
					// UserID index
					"user_id": {
						Name:         "user_id",
						Unique:       false,
						AllowMissing: true,
						Indexer:      &memdb.IntFieldIndex{Field: "UserID"},
					},
					// ChannelID index
					"channel_id": {
						Name:         "channel_id",
						Unique:       false,
						AllowMissing: true,
						Indexer:      &memdb.IntFieldIndex{Field: "ChannelID"},
					},
					// GroupID index
					"group_id": {
						Name:         "group_id",
						Unique:       false,
						AllowMissing: true,
						Indexer:      &memdb.IntFieldIndex{Field: "GroupID"},
					},
					// Compound index: channel_id + group_id
					"channel_group": {
						Name:         "channel_group",
						Unique:       false,
						AllowMissing: true,
						Indexer:      &memdb.StringFieldIndex{Field: "ChannelGroup"},
					},
				},
			},
		},
	}
}

// ── Connection CRUD ──────────────────────────────────────────────

func connID(ws *websocket.Conn) string {
	return fmt.Sprintf("%p", ws)
}

func setConn(ws *websocket.Conn, userID, groupID, channelID int) {
	txn := mdb.Txn(true)
	c := &Connection{
		ID:           connID(ws),
		WS:           ws,
		UserID:       userID,
		GroupID:       groupID,
		ChannelID:    channelID,
		ChannelGroup: fmt.Sprintf("%d,%d", channelID, groupID),
	}
	txn.Insert("connections", c)
	txn.Commit()
}

func delConn(ws *websocket.Conn) {
	txn := mdb.Txn(true)
	raw, err := txn.First("connections", "id", connID(ws))
	if err == nil && raw != nil {
		txn.Delete("connections", raw)
	}
	txn.Commit()
}

func getConn(ws *websocket.Conn) (*Connection, bool) {
	txn := mdb.Txn(false)
	defer txn.Abort()
	raw, err := txn.First("connections", "id", connID(ws))
	if err != nil || raw == nil {
		return nil, false
	}
	return raw.(*Connection), true
}

// broadcast — hedefli mesaj gönderimi
// group_id, channel_id, user_id = 0 ise o filtreyi atla (herkese gönder)
func broadcast(group_id int, channel_id int, user_id int, msg string) {
	txn := mdb.Txn(false)
	defer txn.Abort()

	var it memdb.ResultIterator
	var err error

	// En verimli index'i seç
	if channel_id != 0 && group_id != 0 {
		// Compound index kullan
		key := fmt.Sprintf("%d,%d", channel_id, group_id)
		it, err = txn.Get("connections", "channel_group", key)
	} else if group_id != 0 {
		it, err = txn.Get("connections", "group_id", group_id)
	} else if channel_id != 0 {
		it, err = txn.Get("connections", "channel_id", channel_id)
	} else if user_id != 0 {
		it, err = txn.Get("connections", "user_id", user_id)
	} else {
		// Tüm bağlantılar
		it, err = txn.Get("connections", "id")
	}

	if err != nil {
		log.Println("broadcast query error:", err)
		return
	}

	msgBytes := []byte(msg)
	for obj := it.Next(); obj != nil; obj = it.Next() {
		c := obj.(*Connection)
		// İndex ile daraltılamayan filtreleri burada uygula
		if user_id != 0 && c.UserID != user_id {
			continue
		}
		if group_id != 0 && c.GroupID != group_id {
			continue
		}
		if channel_id != 0 && c.ChannelID != channel_id {
			continue
		}
		c.safeWrite(websocket.TextMessage, msgBytes)
	}
}

func _users(group_id int, channel_id int, user_id int) []*Connection {
	txn := mdb.Txn(false)
	defer txn.Abort()

	var it memdb.ResultIterator
	var err error

	if channel_id != 0 && group_id != 0 {
		key := fmt.Sprintf("%d,%d", channel_id, group_id)
		it, err = txn.Get("connections", "channel_group", key)
	} else if group_id != 0 {
		it, err = txn.Get("connections", "group_id", group_id)
	} else if channel_id != 0 {
		it, err = txn.Get("connections", "channel_id", channel_id)
	} else if user_id != 0 {
		it, err = txn.Get("connections", "user_id", user_id)
	} else {
		it, err = txn.Get("connections", "id")
	}

	if err != nil {
		return nil
	}

	var result []*Connection
	for obj := it.Next(); obj != nil; obj = it.Next() {
		c := obj.(*Connection)
		if user_id != 0 && c.UserID != user_id {
			continue
		}
		if group_id != 0 && c.GroupID != group_id {
			continue
		}
		if channel_id != 0 && c.ChannelID != channel_id {
			continue
		}
		result = append(result, c)
	}
	return result
}

// ── Helpers ──────────────────────────────────────────────────────

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

// ── Main ─────────────────────────────────────────────────────────

func main() {
	// go-memdb oluştur
	schema := createSchema()
	var err error
	mdb, err = memdb.NewMemDB(schema)
	if err != nil {
		log.Fatal("memdb init error:", err)
	}

	// Test modu kontrolü
	testMode = argument("env") == "test"
	if testMode {
		log.Println("⚠ TEST MODE — auth bypass aktif")
	}

	// MySQL bağlantısı (test modunda da açılır, kullanılmazsa sorun yok)
	db, err = sql.Open("mysql", "master:master@tcp(127.0.0.1:3306)/db")
	if err != nil && !testMode {
		log.Fatal(err)
	}
	if db != nil {
		defer db.Close()
	}

	http.HandleFunc("/!direct-signal", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		part := query.Get("part")
		parts := strings.Split(part, ",")
		group_id := ToInt(parts[0])
		channel_id := ToInt(parts[1])
		user_id := ToInt(parts[2])
		message := query.Get("message")
		broadcast(group_id, channel_id, user_id, message)
	})
	http.HandleFunc("/!direct", onlineHandler)
	port := argument("port")
	log.Println("Server started at :" + port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// ── WebSocket Handler ────────────────────────────────────────────

func onlineHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer ws.Close()
	defer delConn(ws)

	// Read deadlines ve pong handler
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Ping ticker — bağlantıyı canlı tut
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			conn, ok := getConn(ws)
			if !ok {
				return
			}
			if err := conn.safeWrite(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}()

	// İlk mesaj: auth token
	_, message, err := ws.ReadMessage()
	if err != nil {
		return
	}
	userID := Func_User_ID(r, string(message))

	setConn(ws, userID, 0, 0)

	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			break
		}

		request := string(message)
		if strings.HasPrefix(request, ":") {
			// Broadcast mesaj gönder
			conn, ok := getConn(ws)
			if ok {
				broadcast(conn.GroupID, conn.ChannelID, 0, request[1:])
			}
		} else {
			// Grup/kanal ayarla
			parts := strings.Split(request, ",")
			setConn(ws, userID, ToInt(parts[0]), ToInt(parts[1]))
		}
	}
}

// ── User Identification ──────────────────────────────────────────

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
	// Test modunda auto-increment ID ver, DB sorgusu atla
	if testMode {
		return int(testUserSeq.Add(1))
	}

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
	ID    int    `db:"id" json:"id"`
	Name  string `db:"name" json:"name"`
	Nick  string `db:"nick" json:"nick"`
	Image string `db:"image" json:"image"`
}

var user_query string = "SELECT id,name,nick,image FROM `user` WHERE id=? AND blocked=0"

func Func_User_Info(id int) *Struct_User {
	var s Struct_User
	db.QueryRow(user_query, id).Scan(
		&s.ID,
		&s.Name,
		&s.Nick,
		&s.Image,
	)
	return &s
}

func argument(a string) string {
	response := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, a+"=") {
			response = strings.Split(arg, "=")[1]
			response = strings.Trim(response, "\"")
		}
	}
	return response
}
