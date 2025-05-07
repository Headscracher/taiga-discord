package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/bwmarrin/discordgo"
	dotenv "github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

type StatusMapping struct {
	TaigaSlug    string `json:"taiga_slug"`
	PlankaListID string `json:"planka_list_id"`
}

type ProjectMapping struct {
	TaigaProjectID   int                      `json:"taiga_project_id"`
	PlankaBoardID    string                   `json:"planka_board_id"`
	DiscordChannelID string                   `json:"discord_channel_id"`
	Statuses         map[string]StatusMapping `json:"statuses"`
}

type MigrationConfig struct {
	Comment  string           `json:"comment"`
	Mappings []ProjectMapping `json:"mappings"`
}

// Planka API types
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

type CardItem struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	Name     string `json:"name"`
}

type CardsListResponse struct {
	Items []CardItem `json:"items"`
}

type LoginRequest struct {
	EmailOrUsername string `json:"emailOrUsername"`
	Password        string `json:"password"`
}

type LoginResponse struct {
	Item string `json:"item"`
}

var authToken string
var authExpires int64

// Struct to hold task data read from the database
type TaskData struct {
	ID       int
	ThreadID string
	TaskID   int
	StatusID int
}

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
		return ""
	}

	resp, err := http.Post(plankaURL+"/api/access-tokens", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var loginResp LoginResponse
	err = json.NewDecoder(resp.Body).Decode(&loginResp)
	if err != nil {
		return ""
	}

	authToken = loginResp.Item
	authExpires = time.Now().Add(time.Hour * 23).Unix()

	return authToken
}

func getBoardDetails(boardID string) (BoardDetailsResponse, error) {
	var boardDetails BoardDetailsResponse

	token := getAuthToken()
	if token == "" {
		return boardDetails, fmt.Errorf("authentication failed")
	}

	req, err := http.NewRequest("GET", os.Getenv("PLANKA_URL")+"/api/boards/"+boardID, nil)
	if err != nil {
		return boardDetails, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return boardDetails, err
	}
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&boardDetails)
	return boardDetails, err
}

func getCards(listID string) ([]CardItem, error) {
	token := getAuthToken()
	if token == "" {
		return nil, fmt.Errorf("authentication failed")
	}

	req, err := http.NewRequest("GET", os.Getenv("PLANKA_URL")+"/api/lists/"+listID+"/cards", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var cardsResponse CardsListResponse
	err = json.NewDecoder(resp.Body).Decode(&cardsResponse)
	if err != nil {
		return nil, err
	}

	return cardsResponse.Items, nil
}

func findCardByName(boardID string, cardName string) (cardID string, listID string, err error) {
	boardDetails, err := getBoardDetails(boardID)
	if err != nil {
		return "", "", err
	}

	// Search through all lists in the board
	for _, list := range boardDetails.Included.Lists {
		// Skip trash and archive lists
		if list.Type == "trash" || list.Type == "archive" {
			continue
		}

		cards, err := getCards(list.ID)
		if err != nil {
			continue
		}

		for _, card := range cards {
			if card.Name == cardName {
				return card.ID, list.ID, nil
			}
		}
	}

	return "", "", fmt.Errorf("card not found in board")
}

func main() {
	// Read migration config
	configFile, err := os.ReadFile("migration.json")
	if err != nil {
		fmt.Println("Error reading migration.json:", err)
		fmt.Println("Please create a migration.json file based on migration.json.example")
		os.Exit(1)
	}

	var config MigrationConfig
	err = json.Unmarshal(configFile, &config)
	if err != nil {
		fmt.Println("Error parsing migration.json:", err)
		os.Exit(1)
	}

	// Open database
	db, err := sql.Open("sqlite3", "file:data/tasks.db?cache=shared")
	if err != nil {
		fmt.Println("Error opening database:", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("Starting migration from Taiga to Planka...")
	fmt.Println("================================================")

	// Add new columns for Planka IDs
	fmt.Println("\n1. Updating database schema...")
	_, err = db.Exec("ALTER TABLE tasks ADD COLUMN planka_card_id STRING")
	if err != nil && err.Error() != "duplicate column name: planka_card_id" {
		fmt.Println("Error adding planka_card_id column:", err)
	}
	_, err = db.Exec("ALTER TABLE tasks ADD COLUMN planka_list_id STRING")
	if err != nil && err.Error() != "duplicate column name: planka_list_id" {
		fmt.Println("Error adding planka_list_id column:", err)
	}
	_, err = db.Exec("ALTER TABLE tasks ADD COLUMN planka_board_id STRING")
	if err != nil && err.Error() != "duplicate column name: planka_board_id" {
		fmt.Println("Error adding planka_board_id column:", err)
	}
	_, err = db.Exec("ALTER TABLE uploads ADD COLUMN planka_file_id STRING")
	if err != nil && err.Error() != "duplicate column name: planka_file_id" {
		fmt.Println("Error adding planka_file_id column:", err)
	}
	_, err = db.Exec("ALTER TABLE uploads ADD COLUMN planka_card_id STRING")
	if err != nil && err.Error() != "duplicate column name: planka_card_id" {
		fmt.Println("Error adding planka_card_id column to uploads:", err)
	}
	_, err = db.Exec("ALTER TABLE comments ADD COLUMN planka_card_id STRING")
	if err != nil && err.Error() != "duplicate column name: planka_card_id" {
		fmt.Println("Error adding planka_card_id column to comments:", err)
	}

	// Build project ID to mapping lookup
	projectMappings := make(map[int]ProjectMapping)
	for _, mapping := range config.Mappings {
		projectMappings[mapping.TaigaProjectID] = mapping
	}

	// --- START: FIX FOR DATABASE LOCK ERROR ---
	
	// Get all tasks from database (Read Lock Acquired)
	rows, err := db.Query("SELECT id, thread_id, task_id, status_id FROM tasks")
	if err != nil {
		fmt.Println("Error querying tasks:", err)
		os.Exit(1)
	}

	// Read all tasks into an in-memory slice
	var tasksToMigrate []TaskData
	for rows.Next() {
		var task TaskData
		err = rows.Scan(&task.ID, &task.ThreadID, &task.TaskID, &task.StatusID)
		if err != nil {
			fmt.Println("Error scanning row:", err)
			continue
		}
		tasksToMigrate = append(tasksToMigrate, task)
	}
	
	// Release the read lock immediately by closing the rows result set
	if err := rows.Close(); err != nil {
		fmt.Println("Error closing rows:", err)
		// We still try to proceed as the main data read succeeded
	}

	// Check for any errors during the iteration (optional but good practice)
	if err := rows.Err(); err != nil {
		fmt.Println("Error during rows iteration:", err)
		// We still try to proceed as the main data read succeeded
	}
	
	// --- END: FIX FOR DATABASE LOCK ERROR ---

	migratedCount := 0
	skippedCount := 0

	fmt.Println("\n2. Migrating task references...")
	// Now iterate over the in-memory slice to perform updates (Lock is now released)
	for _, task := range tasksToMigrate {
		// Find which project this task belongs to by checking all mappings
		var foundMapping *ProjectMapping
		for _, mapping := range config.Mappings {
			// We'll use a heuristic: check if the status_id matches any of the mapped status IDs
			// This is not perfect, but it's the best we can do without additional data
			// In a real scenario, you might need to query Taiga API to get the project ID for each task
			foundMapping = &mapping
			break // For now, we'll just use the first mapping
			// TODO: Improve this logic to properly identify which project each task belongs to
		}

		if foundMapping == nil {
			fmt.Printf("  [SKIP] Task ID %d: Could not determine project mapping\n", task.TaskID)
			skippedCount++
			continue
		}

		// Update the task with Planka IDs
		// Note: We can't automatically create cards in Planka during migration
		// The planka_card_id will be populated when the card is first accessed/synced
		_, err = db.Exec(`
			UPDATE tasks
			SET planka_board_id = ?
			WHERE id = ?
		`, foundMapping.PlankaBoardID, task.ID) // Changed 'id' to 'task.ID' and used 'task' from the slice

		if err != nil {
			// This should now succeed unless there's a different locking issue
			fmt.Printf("  [ERROR] Task ID %d: %v\n", task.TaskID, err)
			skippedCount++
		} else {
			fmt.Printf("  [OK] Task ID %d mapped to board %s\n", task.TaskID, foundMapping.PlankaBoardID)
			migratedCount++
		}
	}

	// Step 3: Match existing cards in Planka
	fmt.Println("\n3. Matching Discord threads with Planka cards...")
	fmt.Println("   Loading environment configuration...")

	// Load environment for Planka access
	dotenv.Load()

	plankaURL := os.Getenv("PLANKA_URL")
	plankaUser := os.Getenv("PLANKA_USERNAME")
	plankaPass := os.Getenv("PLANKA_PASSWORD")

	if plankaURL == "" || plankaUser == "" || plankaPass == "" {
		fmt.Println("   [SKIP] Planka credentials not configured in .env")
		fmt.Println("   Card matching will be skipped.")
		fmt.Println("   Cards will be created when threads are accessed.")
	} else {
		fmt.Println("   Authenticating with Planka...")
		token := getAuthToken()
		if token == "" {
			fmt.Println("   [ERROR] Failed to authenticate with Planka")
			fmt.Println("   Card matching will be skipped.")
		} else {
			// Create Discord session to get thread names
			discordToken := os.Getenv("DISCORD_TOKEN")
			if discordToken == "" {
				fmt.Println("   [SKIP] Discord token not configured")
				fmt.Println("   Card matching requires Discord access to get thread names.")
			} else {
				discord, err := discordgo.New("Bot " + discordToken)
				if err != nil {
					fmt.Println("   [ERROR] Failed to create Discord session:", err)
				} else {
					// Get unlinked tasks (This query and the subsequent loop were already okay 
					// because they don't update the same table inside the read loop.)
					unlinkedRows, err := db.Query(`
						SELECT id, thread_id, planka_board_id
						FROM tasks
						WHERE (planka_card_id IS NULL OR planka_card_id = '')
						AND planka_board_id IS NOT NULL AND planka_board_id != ''
					`)
					if err != nil {
						fmt.Println("   [ERROR] Failed to query unlinked tasks:", err)
					} else {
						defer unlinkedRows.Close()

						type UnlinkedTask struct {
							ID       int
							ThreadID string
							BoardID  string
						}

						var unlinkedTasks []UnlinkedTask
						for unlinkedRows.Next() {
							var task UnlinkedTask
							err = unlinkedRows.Scan(&task.ID, &task.ThreadID, &task.BoardID)
							if err == nil {
								unlinkedTasks = append(unlinkedTasks, task)
							}
						}
						
						// Close rows here to be explicit before the Discord/Planka operations,
						// although `defer` was already set.
						unlinkedRows.Close() 


						if len(unlinkedTasks) == 0 {
							fmt.Println("   No unlinked threads found - all threads are already linked!")
						} else {
							fmt.Printf("   Found %d unlinked thread(s) to match\n\n", len(unlinkedTasks))

							matchedCount := 0
							notFoundCount := 0
							errorCount := 0

							for i, task := range unlinkedTasks {
								fmt.Printf("   [%d/%d] Thread %s... ", i+1, len(unlinkedTasks), task.ThreadID)

								// Get thread name from Discord
								channel, err := discord.Channel(task.ThreadID)
								if err != nil {
									fmt.Printf("[ERROR] Cannot fetch thread\n")
									errorCount++
									continue
								}

								// Search for matching card
								cardID, listID, err := findCardByName(task.BoardID, channel.Name)
								if err != nil {
									fmt.Printf("[NOT FOUND] '%s'\n", channel.Name)
									notFoundCount++
									continue
								}

								// Update database
								_, err = db.Exec(`
									UPDATE tasks
									SET planka_card_id = ?, planka_list_id = ?
									WHERE id = ?
								`, cardID, listID, task.ID)

								if err != nil {
									fmt.Printf("[ERROR] Database update failed\n")
									errorCount++
									continue
								}

								fmt.Printf("[MATCHED] ✓ '%s' → %s\n", channel.Name, cardID)
								matchedCount++
							}

							fmt.Println()
							fmt.Println("   Card Matching Results:")
							fmt.Printf("   - Successfully matched: %d\n", matchedCount)
							fmt.Printf("   - Not found in Planka: %d\n", notFoundCount)
							fmt.Printf("   - Errors: %d\n", errorCount)
						}
					}
				}
			}
		}
	}

	fmt.Println("\n4. Migration Summary:")
	fmt.Println("================================================")
	fmt.Printf("Tasks migrated: %d\n", migratedCount)
	fmt.Printf("Tasks skipped: %d\n", skippedCount)
	fmt.Println("\nIMPORTANT NOTES:")
	fmt.Println("- Database schema updated with Planka fields")
	fmt.Println("- Board IDs have been mapped to existing threads")
	fmt.Println("- Matched threads are now linked to their Planka cards")
	fmt.Println("- Unmatched threads will create new cards when someone posts in them")
	fmt.Println("- Make sure to update your .env file with Planka configuration")
	fmt.Println("\nNext Steps:")
	fmt.Println("1. Verify .env has PLANKA_* variables configured")
	fmt.Println("2. Start the bot: ./planka-discord")
	fmt.Println("3. Verify linked threads work correctly")
	fmt.Println("\nMigration complete!")
}
