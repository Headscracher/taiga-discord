# Discord-Planka Integration Bot

A Discord bot that syncs Discord forum threads with Planka kanban boards. Each Discord thread becomes a Planka card with bidirectional synchronization.

## Features

- **Bidirectional Sync**: Changes in Discord sync to Planka and vice versa
- **Thread → Card**: Discord forum threads automatically create Planka cards
- **Comments**: Thread replies become Planka comments
- **Attachments**: File uploads sync between platforms
- **Status Tracking**: Card list changes update Discord threads
- **Auto-archive**: Completed cards automatically archive Discord threads

## Requirements

- Go 1.20+
- Planka instance with API access
- Discord bot with appropriate permissions
- SQLite (included with go-sqlite3)

## Installation

1. Clone this repository
2. Install dependencies:
```bash
go mod download
```

3. Create a `.env` file with your configuration (see Configuration section)
4. Create the data directory:
```bash
mkdir data
```

5. Build and run:
```bash
go build
./taiga-discord
```

## Configuration

### Environment Variables (.env)

| Variable | Description | Example |
|----------|-------------|---------|
| `DISCORD_TOKEN` | Discord Bot Token | `your_discord_bot_token` |
| `PLANKA_URL` | Planka Base URL | `https://planka.example.com` |
| `PLANKA_USERNAME` | Planka Bot Account Username/Email | `bot@example.com` |
| `PLANKA_PASSWORD` | Planka Bot Account Password | `secure_password` |
| `PLANKA_BOARDS` | Comma-separated list of Planka Board IDs | `1234567890123456,9876543210987654` |
| `[BOARD_ID]_CHANNEL_ID` | Discord Forum Channel ID for each Board | `1234567890123456_CHANNEL_ID=987654321098765432` |
| `[BOARD_ID]_BACKLOG` | Planka List ID for Backlog status | `1234567890123456_BACKLOG=1357924680246802` |
| `[BOARD_ID]_IN_PROGRESS` | Planka List ID for In Progress status | `1234567890123456_IN_PROGRESS=2468135791357913` |
| `[BOARD_ID]_COMPLETED` | Planka List ID for Completed status | `1234567890123456_COMPLETED=3691482570246802` |

### Example .env File

```env
DISCORD_TOKEN=token
PLANKA_URL=https://planka.example.com
PLANKA_USERNAME=bot@example.com
PLANKA_PASSWORD=MySecurePassword123!

# Board 1
PLANKA_BOARDS=1357158568008091264
1357158568008091264_CHANNEL_ID=987654321098765432
1357158568008091264_BACKLOG=1357158568008091265
1357158568008091264_IN_PROGRESS=1357158568008091266
1357158568008091264_COMPLETED=1357158568008091267
```

### Getting Planka IDs

To find the IDs needed for configuration:

1. **Board ID**: Navigate to your board in Planka, the URL will be `/boards/{BOARD_ID}`
2. **List IDs**: Open your browser's developer tools (F12), go to the Network tab, and observe API calls when viewing a board. Look for calls to `/api/boards/{BOARD_ID}` - the response will include all list IDs.

Example API response:
```json
{
  "item": {
    "id": "1357158568008091264"
  },
  "included": {
    "lists": [
      {"id": "1357158568008091265", "name": "To Do", "type": "active"},
      {"id": "1357158568008091266", "name": "In Progress", "type": "active"},
      {"id": "1357158568008091267", "name": "Done", "type": "closed"}
    ]
  }
}
```

## Discord Bot Setup

1. Create a bot at [Discord Developer Portal](https://discord.com/developers/applications)
2. Enable these Privileged Gateway Intents:
   - Message Content Intent
3. Generate and save your bot token
4. Invite the bot to your server with these permissions:
   - Read Messages/View Channels
   - Send Messages
   - Manage Threads
   - Read Message History
   - Attach Files

## Migrating from Taiga

If you're migrating from the Taiga version of this bot:

1. **Backup your database**:
```bash
cp data/tasks.db data/tasks.db.backup
```

2. **Create migration config**:
Copy `migration.json.example` to `migration.json` and fill in your mappings:
```json
{
  "mappings": [
    {
      "taiga_project_id": 1,
      "planka_board_id": "1357158568008091264",
      "discord_channel_id": "987654321098765432",
      "statuses": {
        "backlog": {
          "taiga_slug": "new",
          "planka_list_id": "1357158568008091265"
        },
        "in_progress": {
          "taiga_slug": "in-progress",
          "planka_list_id": "1357158568008091266"
        },
        "completed": {
          "taiga_slug": "done",
          "planka_list_id": "1357158568008091267"
        }
      }
    }
  ]
}
```

3. **Run migration**:
```bash
go run migrate.go
```

4. **Update your .env** file with Planka configuration

5. **Important Notes**:
   - The migration script cannot pre-populate Planka card IDs
   - Existing Discord threads will be linked to new Planka cards on first interaction
   - Consider manually recreating important tasks in Planka before migration
   - Test with a backup database first

## How It Works

### Discord → Planka

1. **New Thread**: Creates a new card in the Backlog list
2. **Thread Name Change**: Updates card name
3. **First Message Edit**: Updates card description
4. **Reply**: Creates a comment on the card
5. **Reply Edit**: Updates the comment
6. **File Upload**: Creates an attachment on the card

### Planka → Discord

1. **Card List Change**: Posts status update message in thread
2. **Card → Completed List**: Archives the Discord thread

## Database Schema

The bot uses SQLite with three tables:

### tasks
- `id`: Primary key
- `thread_id`: Discord thread ID
- `message_id`: Discord message ID
- `planka_card_id`: Planka card ID
- `planka_list_id`: Current Planka list ID
- `planka_board_id`: Planka board ID
- Legacy fields: `task_id`, `status_id` (for migration compatibility)

### comments
- `id`: Primary key
- `message_id`: Discord message ID
- `comment_id`: Planka comment ID
- `planka_card_id`: Planka card ID
- `updated_at`: Timestamp
- Legacy field: `task_id` (for migration compatibility)

### uploads
- `id`: Primary key
- `message_id`: Discord message ID
- `file_id`: Discord attachment ID
- `planka_file_id`: Planka attachment ID
- `planka_card_id`: Planka card ID
- `file_url`: Attachment URL
- Legacy fields: `task_id`, `taiga_file_id` (for migration compatibility)

## Development

### Project Structure

- `main.go`: Main application with Planka integration
- `migrate.go`: Migration tool for Taiga → Planka transition
- `migration.json.example`: Example migration configuration
- `data/`: SQLite database directory
- `.env`: Environment configuration (not in git)

### Building

```bash
go build -o planka-discord
```

### Running in Development

```bash
go run main.go
```

### Migration Tool

```bash
go run migrate.go
```

## Troubleshooting

### Bot not responding
- Check that the bot has proper permissions in Discord
- Verify `DISCORD_TOKEN` is correct
- Ensure the forum channel ID matches configuration

### Cards not syncing
- Verify Planka credentials are correct
- Check that board and list IDs are valid
- Review Planka user has proper permissions

### Migration issues
- Ensure `migration.json` has correct ID mappings
- Check that both Taiga and Planka instances are accessible
- Verify database backup before migration

### Database errors
- Ensure `data/` directory exists
- Check file permissions on `data/tasks.db`
- Verify SQLite is working: `sqlite3 data/tasks.db ".tables"`

## API Documentation

This project uses:
- [Planka API](https://github.com/plankanban/planka/blob/master/server/api/openapi.yml)
- [Discord API via discordgo](https://github.com/bwmarrin/discordgo)

## License

[Your License Here]

## Contributing

Contributions welcome! Please open an issue or submit a pull request.

## Changelog

### v2.0.0 - Planka Migration
- Complete rewrite to use Planka instead of Taiga
- Simplified authentication (no refresh tokens needed)
- Improved error handling
- Added migration tool for Taiga users
- Updated database schema with Planka fields
- Maintained backward compatibility for smooth migration

### v1.0.0 - Initial Taiga Version
- Discord forum thread integration
- Taiga user stories synchronization
- Bidirectional sync
- File attachment support
- Comment synchronization
