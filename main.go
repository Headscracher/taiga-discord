package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type List struct {
	Name string
	ID   string
	Type string
}

type BoardLists map[string][]List

var boardLists BoardLists = make(BoardLists)

var channelBoards map[string]string = make(map[string]string)

func main() {
	dotenv.Load()
	db = initializeDB()
	defer db.Close()

	boards := strings.Split(os.Getenv("PLANKA_BOARDS"), ",")
	for _, board := range boards {
		boardID := strings.TrimSpace(board)
		channelID := os.Getenv(boardID + "_CHANNEL_ID")
		channelBoards[channelID] = boardID

		var lists []List
		lists = append(lists, List{Name: "Backlog", ID: os.Getenv(boardID + "_BACKLOG")})
		lists = append(lists, List{Name: "In Progress", ID: os.Getenv(boardID + "_IN_PROGRESS")})
		lists = append(lists, List{Name: "Completed", ID: os.Getenv(boardID + "_COMPLETED")})
		boardLists[boardID] = lists

		// Verify lists exist in Planka
		verifyLists(boardID)
	}

	discord, err := discordgo.New("Bot " + os.Getenv("DISCORD_TOKEN"))
	discord.AddHandler(changeMessageEvent)
	discord.AddHandler(changeTopicEvent)
	discord.AddHandler(createThreadEvent)
	discord.AddHandler(deleteMessageEvent)
	discord.Identify.Intents = discordgo.IntentGuilds | discordgo.IntentGuildMessages | discordgo.IntentMessageContent

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

type ListResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type BoardDetailsResponse struct {
	Item struct {
		ID string `json:"id"`
	} `json:"item"`
	Included struct {
		Lists []ListResponse `json:"lists"`
	} `json:"included"`
}

func getBoardDetails(boardID string) BoardDetailsResponse {
	authToken := getAuthToken()
	req, err := http.NewRequest("GET", os.Getenv("PLANKA_URL")+"/api/boards/"+boardID, nil)
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

	var boardDetails BoardDetailsResponse
	err = json.NewDecoder(resp.Body).Decode(&boardDetails)
	if err != nil {
		panic(err)
	}
	return boardDetails
}

func verifyLists(boardID string) {
	boardDetails := getBoardDetails(boardID)

	// Verify that configured list IDs exist
	for i, configuredList := range boardLists[boardID] {
		found := false
		for _, apiList := range boardDetails.Included.Lists {
			if apiList.ID == configuredList.ID {
				boardLists[boardID][i].Type = apiList.Type
				found = true
				break
			}
		}
		if !found {
			panic(fmt.Sprintf("List ID %s not found in board %s", configuredList.ID, boardID))
		}
	}
}

func changeTopicEvent(s *discordgo.Session, t *discordgo.ThreadUpdate) {
	thread := t.ID
	channel, err := s.Channel(thread)
	if err != nil {
		fmt.Println("Error getting channel: " + err.Error())
		return
	}
	_, exists := channelBoards[channel.ParentID]
	if !exists {
		return
	}
	row, err := db.Query("SELECT planka_card_id FROM tasks WHERE thread_id = ?", channel.ID)
	if !row.Next() {
		println("No card found")
		return
	}
	var cardID string
	err = row.Scan(&cardID)
	if err != nil {
		panic(err)
	}
	row.Close()
	updateCard(cardID, "", &t.Name, nil)
}

func getBoardID(s *discordgo.Session, thread string) (string, error) {
	channel, err := s.Channel(thread)
	if err != nil {
		return "", err
	}
	boardID, ok := channelBoards[channel.ParentID]
	if !ok {
		return "", errors.New("Could not find board")
	}
	return boardID, nil
}

func changeMessageEvent(s *discordgo.Session, m *discordgo.MessageUpdate) {
	boardID, err := getBoardID(s, m.ChannelID)
	if err != nil {
		return
	}
	attachments := ""
	if len(m.Attachments) > 0 {
		attachments = "\n\nAttachments:"
	}
	row, err := db.Query("SELECT planka_card_id FROM tasks WHERE message_id = ?", m.ID)
	if row.Next() {
		var cardID string
		err = row.Scan(&cardID)
		if err != nil {
			panic(err)
		}
		row.Close()
		for _, attachment := range m.Attachments {
			mdType := "["
			if strings.HasPrefix(attachment.ContentType, "image") {
				mdType = "!["
			}
			uploadedAttachment := attachFile(boardID, attachment, cardID, m.ID)
			attachments += "\n" + mdType + attachment.Filename + "](" + uploadedAttachment + ")"
		}
		content := m.Content + attachments
		updateCard(cardID, m.Author.GlobalName, nil, &content)
		deleteUnusedAttachments(m.Attachments, cardID, m.ID)
	} else {
		row.Close()
		row, err = db.Query("SELECT comment_id, planka_card_id FROM comments WHERE message_id = ?", m.ID)
		if row.Next() {
			var commentID string
			var cardID string
			err = row.Scan(&commentID, &cardID)
			if err != nil {
				panic(err)
			}
			row.Close()
			for _, attachment := range m.Attachments {
				mdType := "["
				if strings.HasPrefix(attachment.ContentType, "image") {
					mdType = "!["
				}
				uploadedAttachment := attachFile(boardID, attachment, cardID, m.ID)
				attachments += "\n" + mdType + attachment.Filename + "](" + uploadedAttachment + ")"
			}
			updateComment(commentID, m.Message, attachments)
			deleteUnusedAttachments(m.Attachments, cardID, m.ID)
		}
	}
}

func deleteMessageEvent(s *discordgo.Session, m *discordgo.MessageDelete) {
	// Check if this is a comment that needs to be deleted from Planka
	row, err := db.Query("SELECT comment_id, planka_card_id FROM comments WHERE message_id = ?", m.ID)
	if err != nil {
		panic(err)
	}
	if row.Next() {
		var commentID string
		var cardID string
		err = row.Scan(&commentID, &cardID)
		if err != nil {
			panic(err)
		}
		row.Close()

		// Delete comment from Planka
		authToken := getAuthToken()
		req, err := http.NewRequest("DELETE", os.Getenv("PLANKA_URL")+"/api/comments/"+commentID, nil)
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

		// Delete attachments from Planka first
		attachRow, err := db.Query("SELECT id, planka_file_id FROM uploads WHERE message_id = ?", m.ID)
		if err != nil {
			panic(err)
		}
		var filesToDelete []FileToDelete
		for attachRow.Next() {
			var uploadID int
			var plankaFileID string
			err = attachRow.Scan(&uploadID, &plankaFileID)
			if err != nil {
				panic(err)
			}
			filesToDelete = append(filesToDelete, FileToDelete{
				ID:           uploadID,
				PlankaFileID: plankaFileID,
			})
		}
		attachRow.Close()

		// Delete each attachment from Planka
		for _, fileToDelete := range filesToDelete {
			req, err := http.NewRequest("DELETE", os.Getenv("PLANKA_URL")+"/api/attachments/"+fileToDelete.PlankaFileID, nil)
			if err != nil {
				panic(err)
			}
			req.Header.Set("Authorization", "Bearer "+authToken)
			client := &http.Client{}
			resp, err := client.Do(req)
			if err != nil {
				panic(err)
			}
			resp.Body.Close()

			// Delete from local database
			_, err = db.Exec("DELETE FROM uploads WHERE id = ?", fileToDelete.ID)
			if err != nil {
				panic(err)
			}
		}

		// Delete comment from local database
		_, err = db.Exec("DELETE FROM comments WHERE message_id = ?", m.ID)
		if err != nil {
			panic(err)
		}
	} else {
		row.Close()
	}
}

type UpdateCardRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

func updateCard(cardID string, user string, name *string, description *string) {
	authToken := getAuthToken()
	var updateReq UpdateCardRequest

	if description != nil {
		updateReq.Description = "Created by " + user + ": \n\n" + *description
	}
	if name != nil {
		updateReq.Name = *name
	}

	body, err := json.Marshal(updateReq)
	if err != nil {
		panic(err)
	}

	req, err := http.NewRequest("PATCH", os.Getenv("PLANKA_URL")+"/api/cards/"+cardID, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		panic(err)
	}

	defer resp.Body.Close()
}

type CommentUpdate struct {
	Text string `json:"text"`
}

func updateComment(commentID string, message *discordgo.Message, attachments string) {
	authToken := getAuthToken()
	comment := CommentUpdate{
		Text: "Comment from " + message.Author.GlobalName + ": \n\n" + message.Content + attachments,
	}
	body, err := json.Marshal(comment)
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("PATCH", os.Getenv("PLANKA_URL")+"/api/comments/"+commentID, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	_, err = db.Exec("UPDATE comments SET updated_at = ? WHERE comment_id = ?", message.EditedTimestamp, commentID)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

func (b *BoardLists) findByID(boardID string, listID string) (List, bool) {
	for _, list := range (*b)[boardID] {
		if list.ID == listID {
			return list, true
		}
	}
	return List{}, false
}

func createThreadEvent(s *discordgo.Session, t *discordgo.MessageCreate) {
	thread := t.ChannelID
	boardID, err := getBoardID(s, thread)
	if err != nil {
		return
	}
	channel, err := s.Channel(thread)
	if err != nil {
		fmt.Println("Error getting channel: " + err.Error())
		return
	}
	// Check if this is the first message in the thread (thread starter message)
	// We check if there's an existing task record for this thread
	row, _ := db.Query("SELECT planka_card_id FROM tasks WHERE thread_id = ?", channel.ID)
	hasExistingTask := row.Next()
	var existingCardID string
	if hasExistingTask {
		row.Scan(&existingCardID)
	}
	row.Close()

	if !hasExistingTask {
		// This is the first message - create a new card
		defaultListID := os.Getenv(boardID + "_BACKLOG")
		cards := getCards(defaultListID)
		cardID := createCard(boardID, defaultListID, t.Author.GlobalName, channel.Name, t.Content, channel.ID, t.ID, s)
		sortCards(defaultListID, cards, cardID)

		// Handle attachments
		attachments := ""
		if len(t.Attachments) > 0 {
			attachments = "\n\nAttachments:"
		}
		for _, attachment := range t.Attachments {
			uploadedAttachment := attachFile(boardID, attachment, cardID, t.ID)
			mdType := "["
			if strings.HasPrefix(attachment.ContentType, "image") {
				mdType = "!["
			}
			attachments += "\n" + mdType + attachment.Filename + "](" + uploadedAttachment + ")"
		}
		updatedContent := t.Content + attachments
		updateCard(cardID, t.Author.GlobalName, nil, &updatedContent)
	} else if existingCardID != "" {
		// Card exists from migration or previous creation - this is a comment
		createComment(boardID, t.Author.GlobalName, channel.ID, t.Message, t.Content, t.ID, t.Attachments)
	} else {
		// Edge case: task exists but no card ID yet (shouldn't happen normally)
		// Treat as first message and create card
		defaultListID := os.Getenv(boardID + "_BACKLOG")
		cards := getCards(defaultListID)
		cardID := createCard(boardID, defaultListID, t.Author.GlobalName, channel.Name, t.Content, channel.ID, t.ID, s)
		sortCards(defaultListID, cards, cardID)

		// Handle attachments
		attachments := ""
		if len(t.Attachments) > 0 {
			attachments = "\n\nAttachments:"
		}
		for _, attachment := range t.Attachments {
			uploadedAttachment := attachFile(boardID, attachment, cardID, t.ID)
			mdType := "["
			if strings.HasPrefix(attachment.ContentType, "image") {
				mdType = "!["
			}
			attachments += "\n" + mdType + attachment.Filename + "](" + uploadedAttachment + ")"
		}
		updatedContent := t.Content + attachments
		updateCard(cardID, t.Author.GlobalName, nil, &updatedContent)
	}
}

type AttachmentResponse struct {
	Item struct {
		ID   string `json:"id"`
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
		Name string `json:"name"`
	} `json:"item"`
}

func attachFile(boardID string, attachment *discordgo.MessageAttachment, cardID string, messageID string) string {
	row, err := db.Query("SELECT file_url FROM uploads WHERE message_id = ? AND file_id = ? AND planka_card_id = ?", messageID, attachment.ID, cardID)
	if err != nil {
		panic(err)
	}
	if row.Next() {
		var fileURL string
		err = row.Scan(&fileURL)
		if err != nil {
			panic(err)
		}
		row.Close()
		return fileURL
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

	// Add type and name fields first
	err = writer.WriteField("type", "file")
	if err != nil {
		panic(err)
	}

	err = writer.WriteField("name", attachment.Filename)
	if err != nil {
		panic(err)
	}

	// Then add the file
	part, err := writer.CreateFormFile("file", attachment.Filename)
	if err != nil {
		panic(err)
	}
	_, err = part.Write(file)
	if err != nil {
		panic(err)
	}

	err = writer.Close()
	if err != nil {
		panic(err)
	}

	req, err := http.NewRequest("POST", os.Getenv("PLANKA_URL")+"/api/cards/"+cardID+"/attachments", formData)
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

	fileURL := os.Getenv("PLANKA_URL") + attachmentResponse.Item.Data.URL
	_, err = db.Exec("INSERT INTO uploads (message_id, file_id, planka_file_id, file_url, planka_card_id) VALUES (?, ?, ?, ?, ?)",
		messageID, attachment.ID, attachmentResponse.Item.ID, fileURL, cardID)
	if err != nil {
		panic(err)
	}
	return fileURL
}

type FileToDelete struct {
	ID            int
	PlankaFileID  string
}

func deleteUnusedAttachments(attachments []*discordgo.MessageAttachment, cardID string, messageID string) {
	authToken := getAuthToken()
	row, err := db.Query("SELECT id, planka_file_id, file_id FROM uploads WHERE planka_card_id = ? AND message_id = ?", cardID, messageID)
	var filesToDelete []FileToDelete
OUTER:
	for row.Next() {
		var uploadID int
		var plankaFileID string
		var fileID string
		err = row.Scan(&uploadID, &plankaFileID, &fileID)
		if err != nil {
			panic(err)
		}
		for _, attachment := range attachments {
			if attachment.ID == fileID {
				continue OUTER
			}
		}
		filesToDelete = append(filesToDelete, FileToDelete{
			ID:           uploadID,
			PlankaFileID: plankaFileID,
		})

	}
	row.Close()
	for _, fileToDelete := range filesToDelete {
		req, err := http.NewRequest("DELETE", os.Getenv("PLANKA_URL")+"/api/attachments/"+fileToDelete.PlankaFileID, nil)
		req.Header.Set("Authorization", "Bearer "+authToken)
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{}
		_, err = client.Do(req)
		if err != nil {
			panic(err)
		}
		_, err = db.Exec("DELETE FROM uploads WHERE id = ?", fileToDelete.ID)
		if err != nil {
			panic(err)
		}
	}
}

type CreateCardRequest struct {
	Type        string `json:"type"`
	Position    float64    `json:"position"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type CardResponse struct {
	Item struct {
		ID       string `json:"id"`
		Position float64    `json:"position"`
		Name     string `json:"name"`
		ListID   string `json:"listId"`
	} `json:"item"`
}

type CardItem struct {
	ID       string `json:"id"`
	Position float64    `json:"position"`
	Name     string `json:"name"`
}

type CardsListResponse struct {
	Items []CardItem `json:"items"`
}

func getCards(listID string) []CardItem {
	authToken := getAuthToken()
	req, err := http.NewRequest("GET", os.Getenv("PLANKA_URL")+"/api/lists/"+listID+"/cards", nil)
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

	var cardsResponse CardsListResponse
	err = json.NewDecoder(resp.Body).Decode(&cardsResponse)
	if err != nil {
		panic(err)
	}

	// Sort by position
	sort.Slice(cardsResponse.Items, func(i, j int) bool {
		return cardsResponse.Items[i].Position < cardsResponse.Items[j].Position
	})

	return cardsResponse.Items
}

func createCard(boardID string, listID string, user string, title string, description string, threadID string, messageID string, discord *discordgo.Session) string {
	authToken := getAuthToken()

	card := CreateCardRequest{
		Type:        "project",
		Position:    65536,
		Name:        title,
		Description: "Created by " + user + ": \n\n" + description,
	}

	body, err := json.Marshal(card)
	if err != nil {
		panic(err)
	}

	req, err := http.NewRequest("POST", os.Getenv("PLANKA_URL")+"/api/lists/"+listID+"/cards", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var cardResponse CardResponse
	err = json.NewDecoder(resp.Body).Decode(&cardResponse)
	if err != nil {
		panic(err)
	}

	_, err = db.Exec("INSERT INTO tasks (thread_id, planka_card_id, planka_list_id, planka_board_id, message_id) VALUES (?, ?, ?, ?, ?)",
		threadID, cardResponse.Item.ID, listID, boardID, messageID)
	if err != nil {
		panic(err)
	}

  discord.ChannelMessageSend(threadID, "Created a new task in Planka: [" +cardResponse.Item.Name + "](<" + os.Getenv("PLANKA_URL") + "/cards/" + cardResponse.Item.ID + ">)")

  // go initializeTaskForAI(cardResponse.Item.ID, boardID, threadID, title, description, discord)
  
	return cardResponse.Item.ID
}

type CommentCreate struct {
	Text string `json:"text"`
}

type CommentCreateResponse struct {
	Item struct {
		ID string `json:"id"`
	} `json:"item"`
}

func createComment(boardID string, user string, threadID string, message *discordgo.Message, content string, messageID string, attachments []*discordgo.MessageAttachment) {
	row, err := db.Query("SELECT planka_card_id FROM tasks WHERE thread_id = ?", threadID)
	if err != nil {
		panic(err)
	}
	if !row.Next() {
		row.Close()
		return
	}
	var cardID string
	err = row.Scan(&cardID)
	if err != nil {
		panic(err)
	}
	row.Close()

	attachmentsMessage := ""
	if len(attachments) > 0 {
		attachmentsMessage = "\n\nAttachments:"
	}
	for _, attachment := range attachments {
		uploadedAttachment := attachFile(boardID, attachment, cardID, messageID)
		mdType := "["
		if strings.HasPrefix(attachment.ContentType, "image") {
			mdType = "!["
		}
		attachmentsMessage += "\n" + mdType + attachment.Filename + "](" + uploadedAttachment + ")"
	}

	authToken := getAuthToken()
	comment := CommentCreate{
		Text: "Comment from " + user + ": \n\n" + content + attachmentsMessage,
	}
	body, err := json.Marshal(comment)
	if err != nil {
		panic(err)
	}

	req, err := http.NewRequest("POST", os.Getenv("PLANKA_URL")+"/api/cards/"+cardID+"/comments", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}

	var commentResponse CommentCreateResponse
	err = json.NewDecoder(resp.Body).Decode(&commentResponse)
	if err != nil {
		panic(err)
	}

	messageID64, err := strconv.ParseInt(message.ID, 10, 64)
	if err != nil {
		panic(err)
	}
	timestamp := messageID64 >> 22
	timestamp = timestamp + 1420070400000

	_, err = db.Exec("INSERT INTO comments (message_id, comment_id, planka_card_id, updated_at) VALUES (?, ?, ?, ?)",
		message.ID, commentResponse.Item.ID, cardID, timestamp)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

func sortCards(listID string, cards []CardItem, newCardID string) {
	// In Planka, we update the position of the new card to be at the top
	// Get the smallest position
	if len(cards) == 0 {
		return
	}

	minPosition := cards[0].Position
	newPosition := minPosition / 2
	if newPosition < 1 {
		newPosition = 1
	}

	authToken := getAuthToken()
	updateReq := map[string]interface{}{
		"position": newPosition,
	}
	body, err := json.Marshal(updateReq)
	if err != nil {
		panic(err)
	}

	req, err := http.NewRequest("PATCH", os.Getenv("PLANKA_URL")+"/api/cards/"+newCardID, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

type LoginRequest struct {
	EmailOrUsername string `json:"emailOrUsername"`
	Password        string `json:"password"`
}

type LoginResponse struct {
	Item string `json:"item"`
}

func initializeDB() *sql.DB {
	db, err := sql.Open("sqlite3", "file:data/tasks.db?cache=shared")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)

	// Create tasks table with Planka fields
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		thread_id STRING,
		message_id STRING,
		task_id INTEGER,
		status_id INTEGER,
		planka_card_id STRING,
		planka_list_id STRING,
		planka_board_id STRING,
		UNIQUE(thread_id, task_id)
	)`)
	if err != nil {
		panic(err)
	}

	// Create comments table with Planka fields
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS comments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		message_id STRING,
		comment_id STRING,
		task_id INTEGER,
		planka_card_id STRING,
		updated_at INTEGER,
		UNIQUE(message_id, comment_id)
	)`)
	if err != nil {
		panic(err)
	}

	// Create uploads table with Planka fields
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS uploads (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id INTEGER,
		planka_card_id STRING,
		message_id STRING,
		file_id STRING,
		taiga_file_id INTEGER,
		planka_file_id STRING,
		file_url STRING
	)`)
	if err != nil {
		panic(err)
	}

	return db
}

var authToken string
var authExpires int64

func getAuthToken() string {
	if authToken != "" && authExpires > time.Now().Unix() {
		return authToken
	}

	plankaURL := os.Getenv("PLANKA_URL")
	login := LoginRequest{
		EmailOrUsername: os.Getenv("PLANKA_USERNAME"),
		Password:        os.Getenv("PLANKA_PASSWORD"),
	}

	body, err := json.Marshal(login)
	if err != nil {
		panic(err)
	}

	resp, err := http.Post(plankaURL+"/api/access-tokens", "application/json", bytes.NewBuffer(body))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var loginResp LoginResponse
	err = json.NewDecoder(resp.Body).Decode(&loginResp)
	if err != nil {
		panic(err)
	}

	authToken = loginResp.Item
	// Planka tokens typically last 24 hours, set expiry to 23 hours to be safe
	authExpires = time.Now().Add(time.Hour * 23).Unix()

	return authToken
}

func checkStatuses(discord *discordgo.Session) {
	for range time.Tick(time.Minute * 1) {
		for boardID, lists := range boardLists {
			for _, list := range lists {
				checkCardList(boardID, list.ID, list.Name, discord)
			}
		}
	}
}

type StatusUpdate struct {
	CardID   string
	ThreadID string
	List     List
}

type CardDetailsResponse struct {
	Item struct {
		ID     string `json:"id"`
		ListID string `json:"listId"`
		Name   string `json:"name"`
	} `json:"item"`
}

func getCard(cardID string) CardDetailsResponse {
	authToken := getAuthToken()
	req, err := http.NewRequest("GET", os.Getenv("PLANKA_URL")+"/api/cards/"+cardID, nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var cardResponse CardDetailsResponse
	err = json.NewDecoder(resp.Body).Decode(&cardResponse)
	if err != nil {
		panic(err)
	}
	return cardResponse
}

func checkCardList(boardID string, listID string, listName string, discord *discordgo.Session) {
	cards := getCards(listID)
	row, err := db.Query("SELECT planka_card_id, thread_id FROM tasks WHERE planka_list_id = ?", listID)
	if err != nil {
		panic(err)
	}
	var statusUpdate []StatusUpdate
OUTER:
	for row.Next() {
		var cardID string
		var threadID string
		err = row.Scan(&cardID, &threadID)
		if err != nil {
			panic(err)
		}
		for _, card := range cards {
			if card.ID == cardID {
				continue OUTER
			}
		}
		// Card moved to different list
		card := getCard(cardID)
		list, found := boardLists.findByID(boardID, card.Item.ListID)
		if !found {
			continue
		}
		statusUpdate = append(statusUpdate, StatusUpdate{
			CardID:   cardID,
			ThreadID: threadID,
			List:     list,
		})
	}
	row.Close()

	for _, update := range statusUpdate {
		discord.ChannelMessageSend(update.ThreadID, "Task status has been updated to \""+update.List.Name+"\"")
		_, err = db.Exec("UPDATE tasks SET planka_list_id = ? WHERE planka_card_id = ?", update.List.ID, update.CardID)
		if err != nil {
			panic(err)
		}

		if update.List.Name == "Completed" {
			val := true
			edit := &discordgo.ChannelEdit{
				Archived: &val,
			}
			_, err = discord.ChannelEdit(update.ThreadID, edit)
			if err != nil {
				panic(err)
			}
		}
	}
}

type AIResponse struct {
  Message *string `json:"message,omitempty"`
  ErrorMessage *string `json:"error,omitempty"`
  Url *string `json:"url,omitempty"`
}

func initializeTaskForAI (taskId string, boardId string, threadId string, title string, description string, discord *discordgo.Session ) {
  aiAssistantEndpoint := os.Getenv("AI_AGENT_ENDPOINT");
  if aiAssistantEndpoint == "" {
    return
  }
  
  body, err := json.Marshal(map[string]string{
    "task_id": taskId,
    "board_id": boardId,
    "title": title,
    "description": description,
  })
	if err != nil {
    return
	}

	req, err := http.NewRequest("POST", aiAssistantEndpoint, bytes.NewBuffer(body))
	if err != nil {
    return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-token", os.Getenv("AI_AGENT_API_TOKEN"))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
    return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error: API returned status %d: %s\n", resp.StatusCode, string(bodyBytes))
		return
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
    return
	}
	var response AIResponse
	err = json.Unmarshal(bodyBytes, &response)
	if err != nil {
		fmt.Printf("Failed to parse JSON response: %v\n", err)
    return
	}
  if response.ErrorMessage != nil {
    println(*response.ErrorMessage)
  }
  if response.Message != nil && *response.Message == "success" {
    discord.ChannelMessageSend(threadId, "The AI assistant has prepared a PR for this task: "+*response.Url)
  }
}
