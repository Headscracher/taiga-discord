package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
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

var db *sql.DB

type Status struct {
	Name string
	Slug string
	Id   int
}

type KanbanStatuses map[int][]Status

var kanbanStatuses KanbanStatuses = make(KanbanStatuses)

var channelProjects map[string]int = make(map[string]int)

func main() {
	dotenv.Load()
	db = initializeDB()
	defer db.Close()


  projects := strings.Split(os.Getenv("TAIGA_PROJECTS"), ",");
  for _, project := range projects {
    var projectStatuses []Status
    projectId, err := strconv.Atoi(project)
    if err != nil {
      panic(err)
    }
    channelId := os.Getenv(project + "_CHANNEL_ID")
    channelProjects[channelId] = projectId 
    projectStatuses = append(projectStatuses, Status{Name: "Backlog", Slug: os.Getenv(project + "_BACKLOG")})
    projectStatuses = append(projectStatuses, Status{Name: "In Progress", Slug: os.Getenv(project + "_IN_PROGRESS")})
    projectStatuses = append(projectStatuses, Status{Name: "Completed", Slug: os.Getenv(project + "_COMPLETED")})
    kanbanStatuses[projectId] = projectStatuses
    setupStatuses(projectId)
  }

	discord, err := discordgo.New("Bot " + os.Getenv("DISCORD_TOKEN"))
	discord.AddHandler(changeMessageEvent)
	discord.AddHandler(changeTopicEvent)
  discord.AddHandler(createThreadEvent)
	discord.Identify.Intents = discordgo.IntentGuilds | discordgo.IntentGuildMessages

	if err != nil {
		panic(err)
	}
	discord.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		fmt.Println("Bot is ready")
	})

	err = discord.Open()

	if err != nil {
		panic(err)
	}

	go checkStatuses(discord)

	//close on ctrl-c
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	discord.Close()
}

type StatusResponse struct {
	Id   int    `json:"id"`
	Slug string `json:"slug"`
}

func setupStatuses(projectId int) {
	authToken := getAuthToken()
	req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL")+"/api/v1/userstory-statuses?project="+strconv.Itoa(projectId), nil)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Authorization", "Bearer "+authToken)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var statuses []StatusResponse
	err = json.NewDecoder(resp.Body).Decode(&statuses)
	if err != nil {
		panic(err)
	}
OUTER:
	for i, kanbanStatus := range kanbanStatuses[projectId] {
		for _, status := range statuses {
			if status.Slug == kanbanStatus.Slug {
				kanbanStatuses[projectId][i].Id = status.Id
				continue OUTER
			}
		}
		panic("Could not find status " + kanbanStatus.Slug)
	}
}

func changeTopicEvent(s *discordgo.Session, t *discordgo.ThreadUpdate) {
	thread := t.ID
	channel, err := s.Channel(thread)
	if err != nil {
		fmt.Println("Error getting channel: " + err.Error())
		return
	}
  _, exists := channelProjects[channel.ParentID]
	if !exists {
		return
	}
	row, err := db.Query("SELECT task_id FROM tasks WHERE thread_id = ?", channel.ID)
	if !row.Next() {
		println("No task found")
		return
	}
	var taskId int
	err = row.Scan(&taskId)
	if err != nil {
		panic(err)
	}
	row.Close()
	updateTask(taskId, "", &t.Name, nil)
}

func getProjectId(s *discordgo.Session, thread string) (int, error) {
	channel, err := s.Channel(thread)
	if err != nil {
    return 0, err;
	}
  project, ok := channelProjects[channel.ParentID]
  if !ok {
    return 0, errors.New("Could not find project")
  }
  return project, nil 
}

func changeMessageEvent(s *discordgo.Session, m *discordgo.MessageUpdate) {
  projectId, err := getProjectId(s, m.ChannelID)
  if err != nil {
    return  
	}
	attachments := ""
	if len(m.Attachments) > 0 {
		attachments = "\n\nAttachments:"
	}
	row, err := db.Query("SELECT task_id FROM tasks WHERE message_id = ?", m.ID)
	if row.Next() {
		var taskId int
		err = row.Scan(&taskId)
		if err != nil {
			panic(err)
		}
		row.Close()
		for _, attachment := range m.Attachments {
			mdType := "["
			if strings.HasPrefix(attachment.ContentType, "image") {
				mdType = "!["
			}
			uploadedAttachment := attachFile(projectId, attachment, taskId, m.ID)
			attachments += "\n" + mdType + attachment.Filename + "](" + uploadedAttachment + ")"
		}
		content := m.Content + attachments
		updateTask(taskId, m.Author.GlobalName, nil, &content)
		deleteUnusedAttachments(m.Attachments, taskId, m.ID)
	} else {
		row.Close()
		row, err = db.Query("SELECT comment_id, task_id FROM comments WHERE message_id = ?", m.ID)
		if row.Next() {
			var commentId string
			var taskId int
			err = row.Scan(&commentId, &taskId)
			if err != nil {
				panic(err)
			}
			row.Close()
			for _, attachment := range m.Attachments {
				mdType := "["
				if strings.HasPrefix(attachment.ContentType, "image") {
					mdType = "!["
				}
				uploadedAttachment := attachFile(projectId, attachment, taskId, m.ID)
				attachments += "\n" + mdType + attachment.Filename + "](" + uploadedAttachment + ")"
			}
			updateComment(commentId, taskId, m.Message, attachments)
			deleteUnusedAttachments(m.Attachments, taskId, m.ID)
		}
	}
}

type UpdateTaskSubjectRequest struct {
	Subject string `json:"subject"`
	Version int    `json:"version"`
}

type UpdateTaskDescriptionRequest struct {
	Description string `json:"description"`
	Version     int    `json:"version"`
}

func getTaskVersion(taskId int) int {
	authToken := getAuthToken()
	req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL")+"/api/v1/userstories/"+strconv.Itoa(taskId), nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var taskResponse TaskResponse
	err = json.NewDecoder(resp.Body).Decode(&taskResponse)
	if err != nil {
		panic(err)
	}
	return taskResponse.Version
}

type NullableString struct {
	val string
}

func updateTask(taskId int, user string, subject *string, content *string) {
	authToken := getAuthToken()
	version := getTaskVersion(taskId)
	var body []byte
	var err error
	if content != nil {
		description := "Created by " + user + ": \n\n" + *content
		task := UpdateTaskDescriptionRequest{
			Description: description,
			Version:     version,
		}
		body, err = json.Marshal(task)
	} else if subject != nil {
		task := UpdateTaskSubjectRequest{
			Subject: *subject,
			Version: version,
		}
		body, err = json.Marshal(task)
	} else {
		panic("No content or subject provided")
	}
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("PATCH", os.Getenv("TAIGA_URL")+"/api/v1/userstories/"+strconv.Itoa(taskId), bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		panic(err)
	}

	defer resp.Body.Close()
}

type EditComment struct {
	Content string `json:"comment"`
}

func updateComment(commentId string, taskId int, message *discordgo.Message, attachments string) {
	authToken := getAuthToken()
	comment := EditComment{
		Content: "Comment from " + message.Author.GlobalName + ": \n\n" + message.Content + attachments,
	}
	body, err := json.Marshal(comment)
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("POST", os.Getenv("TAIGA_URL")+"/api/v1/history/userstory/"+strconv.Itoa(taskId)+"/edit_comment?id="+commentId, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	_, err = db.Exec("UPDATE comments SET updated_at = ? WHERE comment_id = ?", message.EditedTimestamp, commentId)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

}

func (s *KanbanStatuses) findBySlug(projectId int, slug string) Status {
	for _, status := range (*s)[projectId] {
		if status.Slug == slug {
			return status
		}
	}
	panic("Could not find status " + slug)
}

func createThreadEvent (s *discordgo.Session, t *discordgo.MessageCreate) {
	thread := t.ChannelID
  projectId, err := getProjectId(s, thread)
	if err != nil {
		return
	}
	channel, err := s.Channel(thread)
	if err != nil {
		fmt.Println("Error getting channel: " + err.Error())
		return
	}
	if channel.MessageCount == 0 {
		status := kanbanStatuses.findBySlug(projectId, os.Getenv(strconv.Itoa(projectId) + "_BACKLOG")).Id
		tasks := getTasks(projectId, status)
		newTask := createTask(projectId, t.Author.GlobalName, channel.Name, t.Content, channel.ID, t.ID)
		sortTasks(projectId, tasks, newTask, status)
		attachments := ""
		if len(t.Attachments) > 0 {
			attachments = "\n\nAttachments:"
		}
		for _, attachment := range t.Attachments {
			uploadedAttachment := attachFile(projectId, attachment, newTask, t.ID)
			mdType := "["
			if strings.HasPrefix(attachment.ContentType, "image") {
				mdType = "!["
			}
			attachments += "\n" + mdType + attachment.Filename + "](" + uploadedAttachment + ")"
		}
		updatedContent := t.Content + attachments
		updateTask(newTask, t.Author.GlobalName, nil, &updatedContent)
	} else if t.Content != channel.Name {
		createComment(projectId, t.Author.GlobalName, channel.ID, t.Message, t.Content, t.ID, t.Attachments)
	}
}

type AttachmentResponse struct {
	Id      int    `json:"id"`
	Preview string `json:"preview_url"`
}

func attachFile(projectId int, attachment *discordgo.MessageAttachment, taskId int, messageId string) string {
	row, err := db.Query("SELECT file_url FROM uploads WHERE message_id = ? AND file_id = ? AND task_id = ?", messageId, attachment.ID, taskId)
	if err != nil {
		panic(err)
	}
	if row.Next() {
		var fileUrl string
		err = row.Scan(&fileUrl)
		if err != nil {
			panic(err)
		}
		row.Close()
		return fileUrl
	}
	row.Close()
	fileRequest, err := http.Get(attachment.URL)
	if err != nil {
		panic(err)
	}
	file, err := io.ReadAll(fileRequest.Body)
	if err != nil {
		panic(err)
	}
	authToken := getAuthToken()
	formData := new(bytes.Buffer)
	writer := multipart.NewWriter(formData)
	part, err := writer.CreateFormFile("attached_file", attachment.Filename)
	if err != nil {
		panic(err)
	}
	_, err = part.Write(file)
	if err != nil {
		panic(err)
	}
	err = writer.WriteField("object_id", strconv.Itoa(taskId))
	if err != nil {
		panic(err)
	}
	err = writer.WriteField("project", strconv.Itoa(projectId))
	if err != nil {
		panic(err)
	}
	err = writer.Close()
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("POST", os.Getenv("TAIGA_URL")+"/api/v1/userstories/attachments", formData)
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	var attachmentResponse AttachmentResponse
	err = json.NewDecoder(resp.Body).Decode(&attachmentResponse)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	_, err = db.Exec("INSERT INTO uploads (message_id, file_id, taiga_file_id, file_url, task_id) VALUES (?, ?, ?, ?, ?)", messageId, attachment.ID, attachmentResponse.Id, attachmentResponse.Preview, taskId)
	if err != nil {
		panic(err)
	}
	return attachmentResponse.Preview
}

type FileToDelete struct {
	Id          int
	TaigaFileId int
}

func deleteUnusedAttachments(attachments []*discordgo.MessageAttachment, taskId int, messageId string) {
	authToken := getAuthToken()
	row, err := db.Query("SELECT id, taiga_file_id, file_id FROM uploads WHERE task_id = ? AND message_id = ?", taskId, messageId)
	var filesToDelete []FileToDelete
OUTER:
	for row.Next() {
		var uploadId int
		var taigaFileId int
		var fileId string
		err = row.Scan(&uploadId, &taigaFileId, &fileId)
		if err != nil {
			panic(err)
		}
		for _, attachment := range attachments {
			if attachment.ID == fileId {
				continue OUTER
			}
		}
		filesToDelete = append(filesToDelete, FileToDelete{
			Id:          uploadId,
			TaigaFileId: taigaFileId,
		})

	}
	row.Close()
	for _, fileToDelete := range filesToDelete {
		req, err := http.NewRequest("DELETE", os.Getenv("TAIGA_URL")+"/api/v1/userstories/attachments/"+strconv.Itoa(fileToDelete.TaigaFileId), nil)
		req.Header.Set("Authorization", "Bearer "+authToken)
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{}
		_, err = client.Do(req)
		if err != nil {
			panic(err)
		}
		_, err = db.Exec("DELETE FROM uploads WHERE id = ?", fileToDelete.Id)
		if err != nil {
			panic(err)
		}
	}
}

type Task struct {
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Project     int    `json:"project"`
	Status      int    `json:"status"`
	KanbanOrder int    `json:"kanban_order"`
}

type TaskResponse struct {
	Id      int    `json:"id"`
	Prio    int    `json:"kanban_order"`
	Subject string `json:"subject"`
	Version int    `json:"version"`
	Status  int    `json:"status"`
}

func getTasks(projectId int, status int) []TaskResponse {
	authToken := getAuthToken()
	var tasks []TaskResponse
	page := 1
	for true {
		req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL")+"/api/v1/userstories?project="+strconv.Itoa(projectId)+"&status="+strconv.Itoa(status)+"&page_size=100&page="+strconv.Itoa(page), nil)
		if err != nil {
			panic(err)
		}
		req.Header.Set("Authorization", "Bearer "+authToken)
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var taskResponse []TaskResponse
		err = json.NewDecoder(resp.Body).Decode(&taskResponse)
		if err != nil {
			panic(err)
		}
		tasks = append(tasks, taskResponse...)
		itemCount, err := strconv.Atoi(resp.Header.Get("x-pagination-count"))
		if err != nil {
			panic(err)
		}
		pageCount := math.Ceil(float64(itemCount) / 100)
		if itemCount <= 100 || int(pageCount) == page {
			break
		}
		page += 1
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Prio < tasks[j].Prio
	})
	return tasks
}

type CreateTaskResponse struct {
	Id int `json:"id"`
}

func createTask(projectId int, user string, title string, description string, threadId string, messageId string) int {
	authToken := getAuthToken()
	status_id := kanbanStatuses.findBySlug(projectId, os.Getenv(strconv.Itoa(projectId) + "_BACKLOG")).Id
	task := Task{
		Subject:     title,
		Description: "Created by " + user + ": \n\n" + description,
		Project:     projectId,
		Status:      status_id,
		KanbanOrder: 1,
	}
	body, err := json.Marshal(task)
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("POST", os.Getenv("TAIGA_URL")+"/api/v1/userstories", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var taskResponse CreateTaskResponse
	err = json.NewDecoder(resp.Body).Decode(&taskResponse)
	if err != nil {
		panic(err)
	}
	_, err = db.Exec("INSERT INTO tasks (thread_id, task_id, status_id, message_id) VALUES (?, ?, ?, ?)", threadId, taskResponse.Id, status_id, messageId)
	if err != nil {
		panic(err)
	}
	return taskResponse.Id
}

type Comment struct {
	Content string `json:"comment"`
	Version int    `json:"version"`
}

func getTask(taskId int) TaskResponse {
	authToken := getAuthToken()
	req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL")+"/api/v1/userstories/"+strconv.Itoa(taskId), nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var taskResponse TaskResponse
	err = json.NewDecoder(resp.Body).Decode(&taskResponse)
	if err != nil {
		panic(err)
	}
	return taskResponse
}

func checkStatuses(discord *discordgo.Session) {
	for range time.Tick(time.Minute * 1) {
    for projectId, projectStatuses := range kanbanStatuses {
      for _, status := range projectStatuses {
        checkTaskStatus(projectId, status.Id, discord)
      }
    }
	}
}

type StatusUpdate struct {
	TaskId   int
	ThreadId string
	Status   Status
}

func checkTaskStatus(projectId int, status int, discord *discordgo.Session) {
	tasks := getTasks(projectId, status)
	row, err := db.Query("SELECT task_id, thread_id FROM tasks WHERE status_id = ?", status)
	if err != nil {
		panic(err)
	}
	var statusUpdate []StatusUpdate
OUTER:
	for row.Next() {
		var taskId int
		var threadId string
		err = row.Scan(&taskId, &threadId)
		if err != nil {
			panic(err)
		}
		for _, task := range tasks {
			if task.Id == taskId {
				continue OUTER
			}
		}
		task := getTask(taskId)
		for _, status := range kanbanStatuses[projectId] {
			if status.Id == task.Status {
				statusUpdate = append(statusUpdate, StatusUpdate{
					TaskId:   taskId,
					ThreadId: threadId,
					Status:   status,
				})
				break
			}
		}
	}
	row.Close()
	for _, update := range statusUpdate {
		discord.ChannelMessageSend(update.ThreadId, "Task status has been updated to \""+update.Status.Name+"\"")
		_, err = db.Exec("UPDATE tasks SET status_id = ? WHERE task_id = ?", update.Status.Id, update.TaskId)
		if err != nil {
			panic(err)
		}

		if update.Status.Name == "Completed" {
			val := true
			edit := &discordgo.ChannelEdit{
				Archived: &val,
			}
			_, err = discord.ChannelEdit(update.ThreadId, edit)
			if err != nil {
				panic(err)
			}
		}
	}
}

type Attachment struct {
	Name string
	Url  string
}

func createComment(projectId int, user string, threadId string, message *discordgo.Message, content string, messageId string, attachments []*discordgo.MessageAttachment) {
	row, err := db.Query("SELECT task_id FROM tasks WHERE thread_id = ?", threadId)
	if err != nil {
		panic(err)
	}
	if !row.Next() {
		row.Close()
		return
	}
	var taskId int
	err = row.Scan(&taskId)
	if err != nil {
		panic(err)
	}
	row.Close()
	attachmentsMessage := ""
	if len(attachments) > 0 {
		attachmentsMessage = "\n\nAttachments:"
	}
	for _, attachment := range attachments {
		uploadedAttachment := attachFile(projectId, attachment, taskId, messageId)
		mdType := "["
		if strings.HasPrefix(attachment.ContentType, "image") {
			mdType = "!["
		}
		attachmentsMessage += "\n" + mdType + attachment.Filename + "](" + uploadedAttachment + ")"
	}
	authToken := getAuthToken()
	comment := Comment{
		Content: "Comment from " + user + ": \n\n" + content + attachmentsMessage,
		Version: 1,
	}
	body, err := json.Marshal(comment)
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("PATCH", os.Getenv("TAIGA_URL")+"/api/v1/userstories/"+strconv.Itoa(taskId), bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	commentId := getCommentID(taskId)

	messageID, err := strconv.ParseInt(message.ID, 10, 64)
	if err != nil {
		panic(err)
	}
	timestamp := messageID >> 22
	timestamp = timestamp + 1420070400000
	_, err = db.Exec("INSERT INTO comments (message_id, comment_id, task_id, updated_at) VALUES (?, ?, ?, ?)", message.ID, commentId, taskId, timestamp)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

type CommentHistoryResponse struct {
	Id        string `json:"id"`
	Content   string `json:"comment"`
	CreatedAt string `json:"created_at"`
}

func getCommentID(taskId int) string {
	authToken := getAuthToken()
	req, err := http.NewRequest("GET", os.Getenv("TAIGA_URL")+"/api/v1/history/userstory/"+strconv.Itoa(taskId), nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	var historyEntries []CommentHistoryResponse
	err = json.NewDecoder(resp.Body).Decode(&historyEntries)
	if err != nil {
		panic(err)
	}
	sort.Slice(historyEntries, func(i, j int) bool {
		return historyEntries[i].CreatedAt > historyEntries[j].CreatedAt
	})
	defer resp.Body.Close()
	return historyEntries[0].Id
}

type SortRequest struct {
	Project int   `json:"project_id"`
	Stories []int `json:"bulk_userstories"`
	Status  int   `json:"status_id"`
}

func sortTasks(projectId int, tasks []TaskResponse, newTask int, status int) {
	var sortStories []int
	sortStories = append(sortStories, newTask)
	for _, task := range tasks {
		sortStories = append(sortStories, task.Id)
	}
	sortRequest := SortRequest{
		Project: projectId,
		Stories: sortStories,
		Status:  status,
	}
	body, err := json.Marshal(sortRequest)
	if err != nil {
		panic(err)
	}
	authToken := getAuthToken()
	req, err := http.NewRequest("POST", os.Getenv("TAIGA_URL")+"/api/v1/userstories/bulk_update_kanban_order", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

type Auth struct {
	Type     string `json:"type"`
	Pass     string `json:"password"`
	Username string `json:"username"`
}
type AuthResponse struct {
	Token        string `json:"auth_token"`
	RefreshToken string `json:"refresh"`
}

func initializeDB() *sql.DB {
	db, err := sql.Open("sqlite3", "file:data/tasks.db?cache=shared")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, thread_id STRING, message_id STRING, task_id INTEGER, status_id INTEGER, UNIQUE(thread_id, task_id))")
	if err != nil {
		panic(err)
	}
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS comments (id INTEGER PRIMARY KEY AUTOINCREMENT, message_id STRING, comment_id STRING, task_id INTEGER, updated_at INTEGER, UNIQUE(message_id, comment_id))")
	if err != nil {
		panic(err)
	}
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS uploads (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id INTEGER, message_id STRING, file_id STRING, taiga_file_id INTEGER, file_url STRING)")
	if err != nil {
		panic(err)
	}

	return db
}

type AuthTokens struct {
	AuthToken      string
	AuthExpires    int64
	RefreshToken   string
	RefreshExpires int64
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh"`
}

var authTokens AuthTokens

func getAuthToken() string {
	if authTokens.AuthToken != "" && authTokens.AuthExpires > time.Now().Unix() {
		return authTokens.AuthToken
	}
	taigaUrl := os.Getenv("TAIGA_URL")
	if authTokens.AuthToken != "" && authTokens.RefreshExpires > time.Now().Unix() {
		refresh := RefreshRequest{
			RefreshToken: authTokens.RefreshToken,
		}
		body, err := json.Marshal(refresh)
		if err != nil {
			panic(err)
		}
		resp, err := http.Post(taigaUrl+"/api/v1/auth/refresh", "application/json", bytes.NewBuffer(body))
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var authResp AuthResponse
		err = json.NewDecoder(resp.Body).Decode(&authResp)
		if err != nil {
			panic(err)
		}
		authTokens.AuthToken = authResp.Token
		authTokens.AuthExpires = time.Now().Add(time.Hour * 24).Unix()
		return authTokens.AuthToken
	}
	auth := Auth{
		Type:     "normal",
		Pass:     os.Getenv("TAIGA_PASSWORD"),
		Username: os.Getenv("TAIGA_USERNAME"),
	}
	body, err := json.Marshal(auth)
	if err != nil {
		panic(err)
	}
	resp, err := http.Post(taigaUrl+"/api/v1/auth", "application/json", bytes.NewBuffer(body))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var authResp AuthResponse
	err = json.NewDecoder(resp.Body).Decode(&authResp)
	if err != nil {
		panic(err)
	}
	authTokens = AuthTokens{
		AuthToken:      authResp.Token,
		AuthExpires:    time.Now().Add(time.Hour * 24).Unix(),
		RefreshToken:   authResp.RefreshToken,
		RefreshExpires: time.Now().Add(time.Hour * 24 * 8).Unix(),
	}

	return authResp.Token
}
