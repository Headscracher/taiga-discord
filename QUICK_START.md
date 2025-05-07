# Quick Start Guide

## 🚀 Getting Started with Planka Integration

This is a quick reference for setting up and running your Discord-Planka integration bot.

## Prerequisites Checklist

- [ ] Go 1.20+ installed
- [ ] Planka instance running and accessible
- [ ] Discord bot created with token
- [ ] Git repository cloned/updated

## 5-Minute Setup (New Installation)

### 1. Install Dependencies
```bash
go mod download
```

### 2. Create Data Directory
```bash
mkdir -p data
```

### 3. Configure Environment
Create `.env` file:
```env
# Discord
DISCORD_TOKEN=your_discord_bot_token

# Planka
PLANKA_URL=https://planka.example.com
PLANKA_USERNAME=bot@example.com
PLANKA_PASSWORD=your_password

# Boards (comma-separated)
PLANKA_BOARDS=1357158568008091264

# Board Configuration
1357158568008091264_CHANNEL_ID=987654321098765432
1357158568008091264_BACKLOG=1357158568008091265
1357158568008091264_IN_PROGRESS=1357158568008091266
1357158568008091264_COMPLETED=1357158568008091267
```

### 4. Build & Run
```bash
# Build
go build -o planka-discord

# Run
./planka-discord
```

### 5. Test
Create a thread in your Discord forum channel!

## Migration from Taiga (Existing Installation)

### 1. Backup
```bash
cp data/tasks.db data/tasks.db.backup
cp .env .env.backup
```

### 2. Create Migration Config
```bash
cp migration.json.example migration.json
# Edit migration.json with your mappings
```

### 3. Run Migration
```bash
go run migrate.go
```

### 4. Update Configuration
Update `.env` with Planka settings (see template above)

### 5. Build & Test
```bash
go build -o planka-discord
./planka-discord
```

## Finding Planka IDs

### Board ID
1. Open board in Planka
2. Check URL: `/boards/{BOARD_ID}`
3. Copy the ID

### List IDs
1. Open browser DevTools (F12)
2. Go to Network tab
3. View a board
4. Find call to `/api/boards/{BOARD_ID}`
5. Look in response for lists with IDs

Example response:
```json
{
  "included": {
    "lists": [
      {"id": "1357158568008091265", "name": "Backlog"},
      {"id": "1357158568008091266", "name": "In Progress"},
      {"id": "1357158568008091267", "name": "Done"}
    ]
  }
}
```

## Discord Bot Permissions

Required permissions:
- Read Messages/View Channels
- Send Messages
- Manage Threads
- Read Message History
- Attach Files

## Common Issues & Quick Fixes

### Bot won't start
```bash
# Check .env file exists and is valid
cat .env

# Check data directory exists
ls -la data/

# Check for error messages
./planka-discord 2>&1 | tee bot.log
```

### Can't connect to Planka
```bash
# Test Planka URL
curl $PLANKA_URL/api/config

# Verify credentials work
# (Use Planka web interface to log in)
```

### Cards not creating
- Verify board ID is correct
- Check list IDs exist in board
- Ensure bot user has permissions in Planka
- Verify Discord channel ID is correct

## Quick Test Procedure

1. **Start bot**
   ```bash
   ./planka-discord
   ```
   Expected: "Bot is ready"

2. **Create thread** in Discord forum
   Expected: Card appears in Planka Backlog

3. **Post reply** in thread
   Expected: Comment appears in Planka

4. **Upload file** in thread
   Expected: Attachment on Planka card

5. **Move card** in Planka to "In Progress"
   Expected: Status message in Discord

6. **Move card** to "Completed"
   Expected: Thread archives in Discord

## Development Commands

```bash
# Build
go build -o planka-discord

# Run directly
go run main.go

# Build migration tool
go build -o migrate migrate.go

# Run migration
go run migrate.go

# Format code
go fmt ./...

# Run tests (if any)
go test ./...
```

## File Structure

```
taiga-discord/
├── main.go                 # Main application
├── migrate.go              # Migration tool
├── migration.json.example  # Migration config template
├── .env                    # Configuration (not in git)
├── data/                   # Database directory
│   └── tasks.db           # SQLite database
├── README.md              # Main documentation
├── MIGRATION_GUIDE.md     # Migration instructions
├── CHANGES.md             # Technical changes
├── SUMMARY.md             # Migration summary
└── QUICK_START.md         # This file
```

## Useful Commands

```bash
# Check database
sqlite3 data/tasks.db ".tables"

# View tasks
sqlite3 data/tasks.db "SELECT * FROM tasks;"

# Check bot process
ps aux | grep planka-discord

# View bot logs (if running with systemd)
journalctl -u planka-discord -f

# Check Go version
go version

# Check environment
env | grep PLANKA
```

## Environment Variables Reference

| Variable | Required | Example |
|----------|----------|---------|
| `DISCORD_TOKEN` | Yes | `MTIzNDU2...` |
| `PLANKA_URL` | Yes | `https://planka.example.com` |
| `PLANKA_USERNAME` | Yes | `bot@example.com` |
| `PLANKA_PASSWORD` | Yes | `SecurePass123!` |
| `PLANKA_BOARDS` | Yes | `board1,board2` |
| `{BOARD_ID}_CHANNEL_ID` | Yes | `123456789012345678` |
| `{BOARD_ID}_BACKLOG` | Yes | `1357158568008091265` |
| `{BOARD_ID}_IN_PROGRESS` | Yes | `1357158568008091266` |
| `{BOARD_ID}_COMPLETED` | Yes | `1357158568008091267` |

## Running in Production

### Using systemd (Linux)

Create `/etc/systemd/system/planka-discord.service`:
```ini
[Unit]
Description=Discord-Planka Integration Bot
After=network.target

[Service]
Type=simple
User=botuser
WorkingDirectory=/path/to/taiga-discord
ExecStart=/path/to/taiga-discord/planka-discord
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:
```bash
sudo systemctl enable planka-discord
sudo systemctl start planka-discord
sudo systemctl status planka-discord
```

### Using Docker

Create `Dockerfile`:
```dockerfile
FROM golang:1.20-alpine
WORKDIR /app
COPY . .
RUN go build -o planka-discord main.go
CMD ["./planka-discord"]
```

Build and run:
```bash
docker build -t planka-discord .
docker run -d --name planka-discord \
  --env-file .env \
  -v ./data:/app/data \
  planka-discord
```

## Getting Help

1. **Documentation**
   - `README.md` - Comprehensive guide
   - `MIGRATION_GUIDE.md` - Migration help
   - `CHANGES.md` - Technical details

2. **Troubleshooting**
   - Check bot logs
   - Verify .env configuration
   - Test Planka API access
   - Check Discord permissions

3. **Support**
   - Open GitHub issue
   - Include error messages
   - Provide bot logs
   - Share .env (remove sensitive data!)

## Next Steps

After successful setup:

1. ✅ Verify all features work
2. 📚 Read full README.md
3. 🔒 Secure your .env file
4. 📊 Monitor bot for 24-48 hours
5. 📝 Document any custom workflows
6. 🎉 Enjoy your integration!

## Quick Links

- [Main Documentation](README.md)
- [Migration Guide](MIGRATION_GUIDE.md)
- [Technical Changes](CHANGES.md)
- [Migration Summary](SUMMARY.md)
- [Planka API Docs](https://github.com/plankanban/planka)
- [Discord.go Docs](https://github.com/bwmarrin/discordgo)

---

**Ready to go!** 🚀
