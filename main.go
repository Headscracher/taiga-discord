package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"

	"github.com/bwmarrin/discordgo"
	dotenv "github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB;

func main() {
  dotenv.Load();
  db = initializeDB();
  defer db.Close();
  
  discord, err := discordgo.New("Bot " + os.Getenv("DISCORD_TOKEN"));
  if err != nil {
    panic(err);
  }
  discord.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		fmt.Println("Bot is ready")
	})
  // check in channel for new thread
  discord.AddHandler(createThread);
  discord.Identify.Intents = discordgo.IntentGuildMessages;
  err = discord.Open();

  if err != nil {
    panic(err);
  }

  //close on ctrl-c
  c := make(chan os.Signal, 1)
  signal.Notify(c, os.Interrupt)
  <-c
  discord.Close();
}

func createThread(s *discordgo.Session, t *discordgo.MessageCreate) {
  thread := t.ChannelID;
  channel, err := s.Channel(thread);
  if err != nil {
    fmt.Println("Error getting channel: " + err.Error());
    return;
  }
  if (channel.ParentID != os.Getenv("DISCORD_CHANNEL_ID")) {
    return;
  }
  if channel.MessageCount == 0 {
    tasks := getTasks();
    newTask := createTask(t.Author.GlobalName, channel.Name, t.Content, channel.ID);
    sortTasks(tasks, newTask);
  } else {
    createComment(t.Author.GlobalName ,channel.ID, t.Content);
  }
}

type Task struct {
  Subject string `json:"subject"`
  Description string `json:"description"`
  Project int `json:"project"`
  Status int `json:"status"`
  KanbanOrder int `json:"kanban_order"`
}

type TaskResponse struct {
  Id int `json:"id"`
  Prio int `json:"kanban_order"`
  Subject string `json:"subject"`
}

func getTasks() []TaskResponse {
  authToken := getAuthToken();
  req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL") + "/api/v1/userstories?project=8&status=315", nil);
  if err != nil {
    panic(err);
  }
  req.Header.Set("Authorization", "Bearer " + authToken);
  client := &http.Client{};
  resp, err := client.Do(req);
  if err != nil {
    panic(err);
  }
  defer resp.Body.Close();
  var tasks []TaskResponse;
  err = json.NewDecoder(resp.Body).Decode(&tasks);
  if err != nil {
    panic(err);
  }
  sort.Slice(tasks, func(i, j int) bool {
    return tasks[i].Prio < tasks[j].Prio
  });
  return tasks;
}

type CreateTaskResponse struct {
  Id int `json:"id"`
}

func createTask(user string, title string, description string, threadId string) int {
  authToken := getAuthToken(); 
  println(authToken);
  task := Task{
	  Subject: title,
    Description: "Created by " + user + ": \n\n" + description,
	  Project: 8,
	  Status: 315,
	  KanbanOrder: 1,
  }
  body, err := json.Marshal(task);
  if err != nil {
    panic(err);
  }
  req, err := http.NewRequest("POST", os.Getenv("TAIGA_URL") + "/api/v1/userstories", bytes.NewBuffer(body));
  req.Header.Set("Authorization", "Bearer " + authToken);
  req.Header.Set("Content-Type", "application/json");
  client := &http.Client{};
  resp, err := client.Do(req);
  if err != nil {
    panic(err);
  }
  defer resp.Body.Close();
  var taskResponse CreateTaskResponse;
  err = json.NewDecoder(resp.Body).Decode(&taskResponse);
  if err != nil {
    panic(err);
  }
  _, err = db.Exec("INSERT INTO tasks (thread_id, task_id) VALUES (?, ?)", threadId, taskResponse.Id);
  if err != nil {
    panic(err);
  }
  return taskResponse.Id;
}

type Comment struct {
  Content string `json:"comment"`
  Version int `json:"version"`
}

func createComment(user string ,threadId string, content string) {
  row, err := db.Query("SELECT task_id FROM tasks WHERE thread_id = ?", threadId);
  if err != nil {
    panic(err); 
  }
  row.Next();
  var taskId int;
  err = row.Scan(&taskId);
  if err != nil {
    panic(err);
  }
  authToken := getAuthToken();
  comment := Comment{
    Content: "Comment from " + user + ": \n\n" + content,
    Version: 1,
  };
  body, err := json.Marshal(comment);
  if err != nil {
    panic(err);
  }
  req, err := http.NewRequest("PATCH", os.Getenv("TAIGA_URL") + "/api/v1/userstories/" + strconv.Itoa(taskId), bytes.NewBuffer(body));
  req.Header.Set("Authorization", "Bearer " + authToken);
  req.Header.Set("Content-Type", "application/json");
  client := &http.Client{};
  resp, err := client.Do(req);
  if err != nil {
    panic(err);
  }
  defer resp.Body.Close();
}

type SortRequest struct {
  Project int `json:"project_id"`
  Stories []int `json:"bulk_userstories"`
  Status int `json:"status_id"`
}

func sortTasks(tasks []TaskResponse, newTask int) {
  var sortStories []int;
  sortStories = append(sortStories, newTask);
  for _, task := range tasks {
    sortStories = append(sortStories, task.Id);
  }
  sortRequest := SortRequest{
    Project: 8,
    Stories: sortStories,
    Status: 315,
  }
  body, err := json.Marshal(sortRequest);
  if err != nil {
    panic(err);
  }
  authToken := getAuthToken();
  req, err := http.NewRequest("POST", os.Getenv("TAIGA_URL") + "/api/v1/userstories/bulk_update_kanban_order", bytes.NewBuffer(body));
  req.Header.Set("Authorization", "Bearer " + authToken);
  req.Header.Set("Content-Type", "application/json");
  client := &http.Client{};
  resp, err := client.Do(req);
  if err != nil {
    panic(err);
  }
  respBytes, err := io.ReadAll(resp.Body);
  if err != nil {
    panic(err);
  }
  println(string(respBytes));
  defer resp.Body.Close();
}

type Auth struct {
  Type string `json:"type"`
  Pass string `json:"password"`
  Username string `json:"username"`
}
type AuthResponse struct {
  Token string `json:"auth_token"`
}

func initializeDB() *sql.DB{
  db, err := sql.Open("sqlite3", "file:tasks.db")
  if err != nil {
    panic(err);
  }
  _, err = db.Exec("CREATE TABLE IF NOT EXISTS tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, thread_id INTEGER, task_id INTEGER, UNIQUE(thread_id, task_id))");
  if err != nil {
    panic(err);
  }
  return db;
}

func getAuthToken() string {
  taigaUrl := os.Getenv("TAIGA_URL");
  auth := Auth{
    Type: "normal",
    Pass: os.Getenv("TAIGA_PASSWORD"),
    Username: os.Getenv("TAIGA_USERNAME"),
  }
  body, err := json.Marshal(auth);
  if err != nil {
    panic(err);
  }
  resp, err := http.Post(taigaUrl + "/api/v1/auth", "application/json", bytes.NewBuffer(body));
  if err != nil {
    panic(err);
  }
  defer resp.Body.Close();
  var authResp AuthResponse;
  err = json.NewDecoder(resp.Body).Decode(&authResp);
  if err != nil {
    panic(err);
  }
  return authResp.Token;
}
