package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	dotenv "github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB;

type Status struct {
  Name string
  Slug string
  Id int 
}

type KanbanStatuses []Status;
var kanbanStatuses KanbanStatuses;

func main() {
  dotenv.Load();
  db = initializeDB();
  defer db.Close();

  kanbanStatuses = append(kanbanStatuses, Status{Name: "Backlog", Slug: os.Getenv("TAIGA_BACKLOG")});
  kanbanStatuses = append(kanbanStatuses, Status{Name: "In Progress", Slug: os.Getenv("TAIGA_IN_PROGRESS")});
  kanbanStatuses = append(kanbanStatuses, Status{Name: "Completed", Slug: os.Getenv("TAIGA_COMPLETED")});
  setupStatuses();
  
  discord, err := discordgo.New("Bot " + os.Getenv("DISCORD_TOKEN"));
  if err != nil {
    panic(err);
  }
  discord.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		fmt.Println("Bot is ready")
	})
  discord.AddHandler(createThreadEvent);
  discord.AddHandler(changeMessageEvent);
  discord.AddHandler(changeTopicEvent);
  discord.Identify.Intents = discordgo.IntentGuilds | discordgo.IntentGuildMessages;
  err = discord.Open();

  if err != nil {
    panic(err);
  }

  go checkStatuses(discord);

  //close on ctrl-c
  c := make(chan os.Signal, 1)
  signal.Notify(c, os.Interrupt)
  <-c
  discord.Close();
}

type StatusResponse struct {
  Id int `json:"id"`
  Slug string `json:"slug"`
}

func setupStatuses(){
  authToken := getAuthToken();
  req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL") + "/api/v1/userstory-statuses?project=" + os.Getenv("TAIGA_PROJECT_ID"), nil);
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
  var statuses []StatusResponse;
  err = json.NewDecoder(resp.Body).Decode(&statuses);
  if err != nil {
    panic(err);
  }
  OUTER:
  for i, kanbanStatus := range kanbanStatuses {
    for _ , status := range statuses { 
      if status.Slug == kanbanStatus.Slug {
        kanbanStatuses[i].Id = status.Id;
        continue OUTER;
      }
    }
    panic("Could not find status " + kanbanStatus.Slug);
  }
}

func changeTopicEvent(s *discordgo.Session, t *discordgo.ThreadUpdate) {
  thread := t.ID;
  channel, err := s.Channel(thread);
  if err != nil {
    fmt.Println("Error getting channel: " + err.Error());
    return;
  }
  if (channel.ParentID != os.Getenv("DISCORD_CHANNEL_ID")) {
    return;
  }
  row, err := db.Query("SELECT task_id FROM tasks WHERE thread_id = ?", channel.ID);
  if !row.Next() {
    println("No task found");
    return;
  }
  var taskId int;
  err = row.Scan(&taskId);
  if err != nil {
    panic(err);
  }
  row.Close();
  updateTask(taskId, "", &t.Name, nil);
}

func changeMessageEvent(s *discordgo.Session, m *discordgo.MessageUpdate) {
  thread := m.ChannelID;
  channel, err := s.Channel(thread);
  if err != nil {
    fmt.Println("Error getting channel: " + err.Error());
    return;
  }
  if (channel.ParentID != os.Getenv("DISCORD_CHANNEL_ID")) {
    return;
  }
  attachments := "";
  if len(m.Attachments) > 0{
    attachments = "\n\nAttachments:";
  }
  for _, attachment := range m.Attachments  {
    mdType := "[";
    if strings.HasPrefix(attachment.ContentType, "image") {
      mdType = "![";
    }
    attachments += "\n" + mdType + attachment.Filename + "](" + attachment.URL + ")";
  }
  row, err := db.Query("SELECT task_id FROM tasks WHERE message_id = ?", m.ID);
  if row.Next() {
    var taskId int;
    err = row.Scan(&taskId);
    if err != nil {
      panic(err);
    }
    row.Close();
    content := m.Content + attachments;
    updateTask(taskId, m.Author.GlobalName, nil , &content);
  } else {
    row.Close();
    row, err = db.Query("SELECT comment_id, task_id FROM comments WHERE message_id = ?", m.ID);
    if row.Next() {
      var commentId string;
      var taskId int;
      err = row.Scan(&commentId, &taskId);
      if err != nil {
        panic(err);
      }
      row.Close();
      updateComment(commentId, taskId, m.Message, attachments);
    }
  }
}

type UpdateTaskSubjectRequest struct {
  Subject string `json:"subject"`
  Version int `json:"version"`
}

type UpdateTaskDescriptionRequest struct {
  Description string `json:"description"`
  Version int `json:"version"`
}


func getTaskVersion(taskId int) int {
  authToken := getAuthToken();
  req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL") + "/api/v1/userstories/" + strconv.Itoa(taskId), nil);
  req.Header.Set("Authorization", "Bearer " + authToken);
  client := &http.Client{};
  resp, err := client.Do(req);
  if err != nil {
    panic(err);
  }
  defer resp.Body.Close();
  var taskResponse TaskResponse;
  err = json.NewDecoder(resp.Body).Decode(&taskResponse);
  if err != nil {
    panic(err);
  }
  return taskResponse.Version;
}


type NullableString struct {
  val string
}

func updateTask(taskId int, user string, subject *string, content *string) {
  authToken := getAuthToken();
  version := getTaskVersion(taskId);
  var body []byte;
  var err error;
  if content != nil {
    description := "Created by " + user + ": \n\n" + *content;
    task := UpdateTaskDescriptionRequest{
      Description: description,
      Version: version,
    };
    body, err = json.Marshal(task);
  } else if subject != nil {
    task := UpdateTaskSubjectRequest{
      Subject: *subject,
      Version: version,
    }
    body, err = json.Marshal(task);
  } else {
    panic("No content or subject provided");
  }
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


type EditComment struct {
  Content string `json:"comment"`
}

func updateComment(commentId string, taskId int, message *discordgo.Message, attachments string) {
  authToken := getAuthToken();
  comment := EditComment {
    Content: "Comment from " + message.Author.GlobalName + ": \n\n" + message.Content + attachments,
  };
  body, err := json.Marshal(comment);
  if err != nil {
    panic(err);
  }
  req, err := http.NewRequest("POST", os.Getenv("TAIGA_URL") + "/api/v1/history/userstory/" +  strconv.Itoa(taskId) + "/edit_comment?id=" + commentId, bytes.NewBuffer(body));
  req.Header.Set("Authorization", "Bearer " + authToken);
  req.Header.Set("Content-Type", "application/json");
  client := &http.Client{};
  resp, err := client.Do(req);
  if err != nil {
    panic(err);
  }
  _, err = db.Exec("UPDATE comments SET updated_at = ? WHERE comment_id = ?", message.EditedTimestamp, commentId); 
  if err != nil{
    panic(err);
  }
  defer resp.Body.Close();
  
  
}


func (s *KanbanStatuses) findBySlug(slug string) Status {
  for _, status := range *s {
    if status.Slug == slug {
      return status;
    }
  }
  panic("Could not find status " + slug);
}

func createThreadEvent(s *discordgo.Session, t *discordgo.MessageCreate) {
  thread := t.ChannelID;
  channel, err := s.Channel(thread);
  if err != nil {
    fmt.Println("Error getting channel: " + err.Error());
    return;
  }
  if (channel.ParentID != os.Getenv("DISCORD_CHANNEL_ID")) {
    return;
  }
  attachments := "";
  if len(t.Attachments) > 0{
    attachments = "\n\nAttachments:";
  }
  for _, attachment := range t.Attachments  {
    mdType := "[";
    if strings.HasPrefix(attachment.ContentType, "image") {
      mdType = "![";
    }
    attachments += "\n" + mdType + attachment.Filename + "](" + attachment.URL + ")";
  }
  if channel.MessageCount == 0 {
    status := kanbanStatuses.findBySlug(os.Getenv("TAIGA_BACKLOG")).Id;
    tasks := getTasks(status);
    newTask := createTask(t.Author.GlobalName, channel.Name, t.Content + attachments, channel.ID, t.ID);
    sortTasks(tasks, newTask, status);
  } else if t.Content != channel.Name {
    createComment(t.Author.GlobalName, channel.ID, t.Message, t.Content + attachments);
  }
}

func attachFile(fileName string, url string, taskId int){
  fileRequest, err := http.Get(url);
  if err != nil {
    panic(err);
  }
  file, err := io.ReadAll(fileRequest.Body);
  if err != nil {
    panic(err);
  }
  authToken := getAuthToken();
  formData := new(bytes.Buffer);
  writer := multipart.NewWriter(formData);
  part, err := writer.CreateFormFile("attached_file", fileName);
  if err != nil {
    panic(err);
  }
  _, err = part.Write(file);
  if err != nil {
    panic(err);
  }
  err = writer.WriteField("object_id", strconv.Itoa(taskId));
  if err != nil {
    panic(err);
  }
  err = writer.WriteField("project", os.Getenv("TAIGA_PROJECT_ID"));
  if err != nil {
    panic(err);
  }
  err = writer.Close();
  if err != nil {
    panic(err);
  }
  println("Attaching file " + fileName + " to task " + strconv.Itoa(taskId));
  req, err := http.NewRequest("POST", os.Getenv("TAIGA_URL") + "/api/v1/userstories/attachments", formData);
  req.Header.Set("Authorization", "Bearer " + authToken);
  req.Header.Set("Content-Type", writer.FormDataContentType())
  client := &http.Client{};
  resp, err := client.Do(req);
  if err != nil {
    panic(err);
  }
  data, err := io.ReadAll(resp.Body);
  if err != nil {
    panic(err);
  }
  println(string(data));
  defer resp.Body.Close();
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
  Version int `json:"version"`
  Status int `json:"status"`
}

func getTasks(status int) []TaskResponse {
  authToken := getAuthToken();
  var tasks []TaskResponse;
  page := 1;
  for true {
    req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL") + "/api/v1/userstories?project=" + os.Getenv("TAIGA_PROJECT_ID") +  "&status=" + strconv.Itoa(status) + "&page_size=100&page=" + strconv.Itoa(page), nil);
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
    var taskResponse []TaskResponse;
    err = json.NewDecoder(resp.Body).Decode(&taskResponse);
    if err != nil {
      panic(err);
    }
    tasks = append(tasks, taskResponse...);
    itemCount, err := strconv.Atoi(resp.Header.Get("x-pagination-count"));
    if err != nil {
      panic(err);
    }
    pageCount := math.Ceil(float64(itemCount)/100);
    if itemCount <= 100 || int(pageCount) == page {
      break;
    }
    page += 1;
  }
  sort.Slice(tasks, func(i, j int) bool {
    return tasks[i].Prio < tasks[j].Prio
  });
  return tasks;
}

type CreateTaskResponse struct {
  Id int `json:"id"`
}

func createTask(user string, title string, description string, threadId string, messageId string) int {
  authToken := getAuthToken(); 
  status_id := kanbanStatuses.findBySlug(os.Getenv("TAIGA_BACKLOG")).Id;
  projectId, err := strconv.Atoi(os.Getenv("TAIGA_PROJECT_ID"));
  if err != nil {
    panic(err);
  }
  task := Task{
	  Subject: title,
    Description: "Created by " + user + ": \n\n" + description,
	  Project: projectId,
	  Status: status_id,
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
  _, err = db.Exec("INSERT INTO tasks (thread_id, task_id, status_id, message_id) VALUES (?, ?, ?, ?)", threadId, taskResponse.Id, status_id, messageId);
  if err != nil {
    panic(err);
  }
  return taskResponse.Id;
}

type Comment struct {
  Content string `json:"comment"`
  Version int `json:"version"`
}

func getTask(taskId int) TaskResponse {
  authToken := getAuthToken();
  req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL") + "/api/v1/userstories/" + strconv.Itoa(taskId), nil);
  req.Header.Set("Authorization", "Bearer " + authToken);
  client := &http.Client{};
  resp, err := client.Do(req);
  if err != nil {
    panic(err);
  }
  defer resp.Body.Close();
  var taskResponse TaskResponse;
  err = json.NewDecoder(resp.Body).Decode(&taskResponse);
  if err != nil {
    panic(err);
  }
  return taskResponse;
}

func checkStatuses(discord *discordgo.Session) {
  for range time.Tick(time.Minute * 1) {
    for _, status := range kanbanStatuses { 
      checkTaskStatus(status.Id, discord);
    }
  }
}

type StatusUpdate struct {
  TaskId int
  ThreadId string
  Status Status
}

func checkTaskStatus(status int, discord *discordgo.Session) {
  tasks := getTasks(status);
  row, err := db.Query("SELECT task_id, thread_id FROM tasks WHERE status_id = ?", status);
  if err != nil {
    panic(err);
  }
  var statusUpdate []StatusUpdate;
  OUTER:
  for row.Next() {
    var taskId int;
    var threadId string;
    err = row.Scan(&taskId, &threadId);
    if err != nil {
      panic(err);
    }
    for _, task := range tasks {
      if task.Id == taskId {
        continue OUTER;
      }
    }
    task := getTask(taskId);
    for _, status := range kanbanStatuses {
      if status.Id == task.Status {
        statusUpdate = append(statusUpdate, StatusUpdate{
          TaskId: taskId,
          ThreadId: threadId,
          Status: status,
        });
        break;
      }
    }
  }
  row.Close();
  for _, update := range statusUpdate {
    discord.ChannelMessageSend(update.ThreadId, "Task status has been updated to \"" + update.Status.Name + "\"");
    _, err = db.Exec("UPDATE tasks SET status_id = ? WHERE task_id = ?", update.Status.Id, update.TaskId);
    if err != nil {
      panic(err);
    }
  }
}

func createComment(user string ,threadId string, message *discordgo.Message, content string) {
  row, err := db.Query("SELECT task_id FROM tasks WHERE thread_id = ?", threadId);
  if err != nil {
    panic(err); 
  }
  if !row.Next() {
    row.Close();
    return;
  }
  var taskId int;
  err = row.Scan(&taskId);
  if err != nil {
    panic(err);
  }
  row.Close();
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
  commentId := getCommentID(taskId);

  messageID, err := strconv.ParseInt(message.ID, 10, 64); 
  if err != nil {
    panic(err);
  }
  timestamp := messageID >> 22;
  timestamp = timestamp + 1420070400000;
  _, err = db.Exec("INSERT INTO comments (message_id, comment_id, task_id, updated_at) VALUES (?, ?, ?, ?)", message.ID, commentId, taskId, timestamp);
  if err != nil {
    panic(err);
  }
  defer resp.Body.Close();
}

type CommentHistoryResponse struct {
  Id string `json:"id"`
  Content string `json:"comment"`
  CreatedAt string `json:"created_at"`
}

func getCommentID(taskId int) string{
  authToken := getAuthToken();
  req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL") + "/api/v1/history/userstory/" + strconv.Itoa(taskId), nil);
  req.Header.Set("Authorization", "Bearer " + authToken);
  req.Header.Set("Content-Type", "application/json");
  client := &http.Client{};
  resp, err := client.Do(req);
  if err != nil {
    panic(err);
  }
  var historyEntries []CommentHistoryResponse;
  err = json.NewDecoder(resp.Body).Decode(&historyEntries);
  if err != nil {
    panic(err);
  }
  sort.Slice(historyEntries, func(i, j int) bool {
    return historyEntries[i].CreatedAt > historyEntries[j].CreatedAt
  });
  defer resp.Body.Close();
  return historyEntries[0].Id;
}

type SortRequest struct {
  Project int `json:"project_id"`
  Stories []int `json:"bulk_userstories"`
  Status int `json:"status_id"`
}

func sortTasks(tasks []TaskResponse, newTask int, status int) {
  var sortStories []int;
  sortStories = append(sortStories, newTask);
  for _, task := range tasks {
    sortStories = append(sortStories, task.Id);
  }
  projectId, err := strconv.Atoi(os.Getenv("TAIGA_PROJECT_ID"));
  if err != nil {
    panic(err);
  }
  sortRequest := SortRequest{
    Project: projectId,
    Stories: sortStories,
    Status: status,
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
  defer resp.Body.Close();
}

type Auth struct {
  Type string `json:"type"`
  Pass string `json:"password"`
  Username string `json:"username"`
}
type AuthResponse struct {
  Token string `json:"auth_token"`
  RefreshToken string `json:"refresh"`
}

func initializeDB() *sql.DB{
  db, err := sql.Open("sqlite3", "file:data/tasks.db?cache=shared")
  if err != nil {
    panic(err);
  }
  db.SetMaxOpenConns(1);
  _, err = db.Exec("CREATE TABLE IF NOT EXISTS tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, thread_id STRING, message_id STRING, task_id INTEGER, status_id INTEGER, UNIQUE(thread_id, task_id))");
  if err != nil {
    panic(err);
  }
  _, err = db.Exec("CREATE TABLE IF NOT EXISTS comments (id INTEGER PRIMARY KEY AUTOINCREMENT, message_id STRING, comment_id STRING, task_id INTEGER, updated_at INTEGER, UNIQUE(message_id, comment_id))");
  return db;
}

type AuthTokens struct {
  AuthToken string
  AuthExpires int64
  RefreshToken string
  RefreshExpires int64
}

type RefreshRequest struct {
  RefreshToken string `json:"refresh"`
}

var authTokens AuthTokens;

func getAuthToken() string {
  if authTokens.AuthToken != ""  && authTokens.AuthExpires > time.Now().Unix(){
    return authTokens.AuthToken;
  }
  taigaUrl := os.Getenv("TAIGA_URL");
  if authTokens.AuthToken != "" && authTokens.RefreshExpires > time.Now().Unix(){
    refresh := RefreshRequest{
      RefreshToken: authTokens.RefreshToken,
    }
    body, err := json.Marshal(refresh);
    if err != nil {
      panic(err);
    }
    resp, err := http.Post(taigaUrl + "/api/v1/auth/refresh", "application/json", bytes.NewBuffer(body));
    if err != nil {
      panic(err);
    }
    defer resp.Body.Close();
    var authResp AuthResponse;
    err = json.NewDecoder(resp.Body).Decode(&authResp);
    if err != nil {
      panic(err);
    }
    authTokens.AuthToken = authResp.Token;
    authTokens.AuthExpires = time.Now().Add(time.Hour * 24).Unix();
    return authTokens.AuthToken;
  }
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
  authTokens = AuthTokens{
    AuthToken: authResp.Token,
    AuthExpires: time.Now().Add(time.Hour * 24).Unix(),
    RefreshToken: authResp.RefreshToken,
    RefreshExpires: time.Now().Add(time.Hour * 24 * 8).Unix(),
  }

  return authResp.Token;
}
