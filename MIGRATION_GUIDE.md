# Migration Guide: Taiga → Planka

This guide will help you migrate from the Taiga-based Discord bot to the Planka-based version.

## Overview

The migration process involves:
1. Setting up your Planka instance and boards
2. Backing up your existing database
3. Mapping Taiga projects/statuses to Planka boards/lists
4. Running the migration script
5. Updating your environment configuration
6. Testing the new integration

## Prerequisites

- Working Taiga Discord bot installation
- Planka instance with admin access
- Go 1.20+ installed
- Backup of your data

## Step-by-Step Migration

### Step 1: Prepare Planka

1. **Create Planka boards** corresponding to your Taiga projects
2. **Create lists** in each board (typically: Backlog, In Progress, Completed)
3. **Create a bot user account** in Planka with appropriate permissions
4. **Note down IDs**:
   - Board IDs (from URL: `/boards/{BOARD_ID}`)
   - List IDs (from API response - see README for details)

### Step 2: Backup Everything

```bash
# Backup database
cp data/tasks.db data/tasks.db.backup

# Backup .env
cp .env .env.backup

# Optional: Backup the entire directory
tar -czf taiga-discord-backup-$(date +%Y%m%d).tar.gz .
```

### Step 3: Create Migration Configuration

1. Copy the example configuration:
```bash
cp migration.json.example migration.json
```

2. Edit `migration.json` with your mappings:
```json
{
  "comment": "Migration configuration for converting Taiga project IDs and status slugs to Planka board IDs and list IDs",
  "mappings": [
    {
      "taiga_project_id": 1,
      "planka_board_id": "1357158568008091264",
      "discord_channel_id": "123456789012345678",
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

**Finding Your Values:**

- **taiga_project_id**: Check your old `.env` file for `TAIGA_PROJECTS`
- **planka_board_id**: Navigate to the board in Planka, check the URL
- **discord_channel_id**: Right-click channel in Discord → Copy ID (Developer Mode must be enabled)
- **taiga_slug**: Check your old `.env` for `[PROJECT_ID]_BACKLOG`, etc.
- **planka_list_id**: Use browser DevTools to inspect API responses

### Step 4: Run Migration Script

```bash
# First, do a dry run on the backup
cp data/tasks.db data/tasks-test.db
# Edit migrate.go temporarily to use tasks-test.db
go run migrate.go

# If everything looks good, run on the real database
go run migrate.go
```

Expected output:
```
Starting migration from Taiga to Planka...
================================================

1. Updating database schema...

2. Migrating task references...
  [OK] Task ID 123 mapped to board 1357158568008091264
  [OK] Task ID 456 mapped to board 1357158568008091264
  [SKIP] Task ID 789: Could not determine project mapping

3. Migration Summary:
================================================
Tasks migrated: 25
Tasks skipped: 2

IMPORTANT NOTES:
- Planka card IDs cannot be pre-populated during migration
- Existing Discord threads will be linked to new Planka cards on first sync
- You may want to manually review and recreate important tasks in Planka
- Make sure to update your .env file with Planka configuration

Migration complete!
```

### Step 5: Update Environment Configuration

1. Create new `.env` based on Planka requirements:

```env
# Discord Configuration (unchanged)
DISCORD_TOKEN=your_discord_token_here

# Planka Configuration (NEW)
PLANKA_URL=https://your-planka-instance.com
PLANKA_USERNAME=bot@example.com
PLANKA_PASSWORD=your_secure_password

# Board Configuration (CHANGED FROM TAIGA)
PLANKA_BOARDS=1357158568008091264,9876543210987654

# Board 1 Configuration
1357158568008091264_CHANNEL_ID=987654321098765432
1357158568008091264_BACKLOG=1357158568008091265
1357158568008091264_IN_PROGRESS=1357158568008091266
1357158568008091264_COMPLETED=1357158568008091267

# Board 2 Configuration (if you have multiple boards)
9876543210987654_CHANNEL_ID=123456789012345678
9876543210987654_BACKLOG=2468135791357913579
9876543210987654_IN_PROGRESS=1357924680246802468
9876543210987654_COMPLETED=9876543210123456789
```

### Step 6: Test the New Integration

1. **Stop the old bot** if it's still running

2. **Rebuild the application**:
```bash
go build -o planka-discord
```

3. **Run the new bot**:
```bash
./planka-discord
```

4. **Test basic functionality**:
   - Create a new thread in Discord → Should create a card in Planka
   - Post a comment in the thread → Should appear as comment in Planka
   - Upload a file → Should attach to the Planka card
   - Change card list in Planka → Should update Discord thread
   - Move card to "Completed" → Should archive Discord thread

### Step 7: Handle Existing Threads

The migration cannot automatically create Planka cards for existing Discord threads. You have two options:

#### Option A: Manual Recreation (Recommended for important tasks)
1. Create cards manually in Planka
2. Use the original Discord thread content as reference
3. Update the database to link them (advanced)

#### Option B: Natural Migration
1. Let existing threads naturally sync on next interaction
2. When someone posts in an old thread, a new card will be created
3. Historical context will be preserved in Discord

## Troubleshooting

### Migration Script Errors

**Error: "Error reading migration.json"**
- Ensure `migration.json` exists in the project root
- Check JSON syntax with a validator

**Error: "Error opening database"**
- Verify `data/tasks.db` exists
- Check file permissions

**Error: "Could not determine project mapping"**
- Ensure all your Taiga projects are included in `migration.json`
- Check that `taiga_project_id` values are correct

### Bot Connection Issues

**Bot won't start with new configuration**
- Verify Planka URL is correct and accessible
- Check Planka username/password
- Ensure board and list IDs exist in Planka

**Cards not being created**
- Check that the Planka user has write permissions on the board
- Verify list IDs are correct
- Check bot logs for API errors

### Data Synchronization Issues

**Existing threads not working**
- This is expected - see "Handle Existing Threads" section
- Post a new message to trigger sync

**Files not uploading**
- Check Planka attachment size limits
- Verify bot user has upload permissions
- Check Planka storage configuration

## Rollback Procedure

If you need to rollback to Taiga:

```bash
# Stop the new bot
pkill planka-discord

# Restore database backup
cp data/tasks.db.backup data/tasks.db

# Restore old .env
cp .env.backup .env

# Rebuild old version
git checkout <old-taiga-commit>
go build

# Start old bot
./taiga-discord
```

## Post-Migration Checklist

- [ ] All boards configured in `.env`
- [ ] Migration script ran successfully
- [ ] Bot connects to Discord
- [ ] Bot authenticates with Planka
- [ ] New threads create cards
- [ ] Comments sync properly
- [ ] Files upload correctly
- [ ] Status changes update Discord
- [ ] Completed cards archive threads
- [ ] Old database backed up
- [ ] Old configuration saved
- [ ] Team informed of changes

## Key Differences: Taiga vs Planka

| Feature | Taiga | Planka |
|---------|-------|---------|
| **Authentication** | Token + Refresh Token | Simple JWT Token |
| **Structure** | Projects → User Stories | Projects → Boards → Lists → Cards |
| **IDs** | Integer IDs | Snowflake String IDs |
| **Status** | Status ID on User Story | List ID (card location) |
| **Comments** | History API | Comments API |
| **Attachments** | /userstories/attachments | /cards/{id}/attachments |
| **Sorting** | bulk_update_kanban_order | Individual position updates |

## Additional Resources

- [Planka API Documentation](https://github.com/plankanban/planka/blob/master/server/api/openapi.yml)
- [Main README](README.md)
- [Discord.js Documentation](https://discord.js.org/)

## Support

If you encounter issues during migration:

1. Check this guide thoroughly
2. Review the main README.md
3. Check Discord bot permissions
4. Verify Planka API access
5. Open an issue on GitHub with:
   - Error messages
   - Migration script output
   - Bot logs (remove sensitive data)

## Success!

Once migration is complete and tested:

1. Keep your backup for at least a week
2. Monitor the bot for any issues
3. Inform your team of any workflow changes
4. Consider updating your documentation
5. Enjoy your new Planka integration!
