# Card Matching Feature

## Overview

The migration tool includes **bulk card matching** to automatically link existing Discord threads to their corresponding Planka cards during migration. This happens as part of the `migrate` tool, not at runtime, making the bot simpler and faster.

## How It Works

### Migration-Time Matching (Recommended Approach)

**When:** During migration via `./migrate` command

**Process:**
1. Run `./migrate` with `.env` configured
2. Migration updates database schema
3. Maps board IDs to threads
4. **Automatically searches Planka** for all existing threads
5. Links threads to matching cards in database
6. Bot uses these links at runtime

**Advantages:**
- ✅ All matching happens upfront during migration
- ✅ Runtime bot is simpler and faster
- ✅ Clear visibility into what was matched
- ✅ Easy to retry if needed

### Workflow

```
Step 1: Prepare Planka
   Create cards with names matching Discord thread titles
         ↓
Step 2: Run Migration
   ./migrate
         ↓
   Reads migration.json config
         ↓
   Updates database schema
         ↓
   Maps board IDs to threads
         ↓
   FOR EACH unlinked thread:
     - Gets thread name from Discord
     - Searches Planka for matching card name
     - If found: Links in database
     - If not found: Skips (will create new card later)
         ↓
Step 3: Bot Runtime
   ./planka-discord
         ↓
   FOR EACH thread interaction:
     - Checks database for planka_card_id
     - If found: Uses existing card
     - If not found: Creates new card
```

## Using the Migration Tool

### Prerequisites

1. **`.env` file configured** with Planka and Discord credentials
2. **`migration.json`** created with project → board mappings
3. **Cards created in Planka** (optional, for matching)

### Running Migration

```bash
# Ensure .env is configured
cat .env
DISCORD_TOKEN=...
PLANKA_URL=...
PLANKA_USERNAME=...
PLANKA_PASSWORD=...
PLANKA_BOARDS=...

# Run migration
./migrate
```

### Migration Output Example

```
Starting migration from Taiga to Planka...
================================================

1. Updating database schema...

2. Migrating task references...
  [OK] Task ID 123 mapped to board 1357158568008091264
  [OK] Task ID 456 mapped to board 1357158568008091264

3. Matching Discord threads with Planka cards...
   Loading environment configuration...
   Authenticating with Planka...
   Found 25 unlinked thread(s) to match

   [1/25] Thread 987654321... [MATCHED] ✓ 'Fix login bug' → 1357158568008091265
   [2/25] Thread 123456789... [NOT FOUND] 'Update API docs'
   [3/25] Thread 555666777... [MATCHED] ✓ 'Add dark mode' → 1357158568008091266
   ...

   Card Matching Results:
   - Successfully matched: 18
   - Not found in Planka: 7
   - Errors: 0

4. Migration Summary:
================================================
Tasks migrated: 25
Tasks skipped: 0

Next Steps:
1. Verify .env has PLANKA_* variables configured
2. Start the bot: ./planka-discord
3. Verify linked threads work correctly

Migration complete!
```

## Matching Strategy

### Exact Name Match

- **Case-sensitive** string matching
- Thread name must exactly match card name
- No fuzzy matching or partial matches

**Examples:**
- ✅ "Fix login bug" matches "Fix login bug"
- ❌ "Fix login bug" does NOT match "fix login bug" (case)
- ❌ "Fix login bug" does NOT match "Fix login bug " (trailing space)

### Search Scope

The migration tool searches:
- ✅ All active lists in the configured board
- ✅ Closed lists
- ❌ Trash lists (skipped)
- ❌ Archive lists (skipped)

### Unmatched Threads

If a thread name has no matching card in Planka:
- Marked as `[NOT FOUND]` in migration output
- `planka_card_id` remains NULL in database
- Bot will create new card when someone posts in the thread

## Preparation Strategies

### Option 1: Pre-Create Cards (Recommended)

**Best for:** Important threads you want to preserve

**Process:**
1. Export Discord thread names
2. Create matching cards in Planka
3. Run migration
4. High match rate

**Benefits:**
- Control over which lists cards go in
- Can set descriptions, labels, etc.
- Everything linked automatically

### Option 2: Let Bot Create (Lazy)

**Best for:** Less important threads

**Process:**
1. Run migration without pre-creating cards
2. Start bot
3. New cards created when threads are accessed

**Benefits:**
- Less upfront work
- Cards only created for active threads

### Option 3: Hybrid

**Best for:** Most migrations

**Process:**
1. Pre-create cards for important threads
2. Run migration (matches important ones)
3. Let bot create cards for others

**Benefits:**
- Best of both approaches
- Minimal effort, good results

## Code Implementation

### Location

**File:** `migrate.go`

**Function:** Step 3 of main() starting at line ~305

### Key Functions

**`getAuthToken()`** - Authenticates with Planka
```go
func getAuthToken() string
```

**`getBoardDetails(boardID)`** - Fetches board and lists
```go
func getBoardDetails(boardID string) (BoardDetailsResponse, error)
```

**`getCards(listID)`** - Fetches cards from a list
```go
func getCards(listID string) ([]CardItem, error)
```

**`findCardByName(boardID, cardName)`** - Searches for card
```go
func findCardByName(boardID string, cardName string) (cardID string, listID string, err error)
```

### Runtime Bot Logic

**File:** `main.go`

**Function:** `createThreadEvent()` at line ~327

**Simplified logic:**
```go
// Check database for existing planka_card_id
if cardID exists in database {
    Use existing card  // Linked during migration
} else {
    Create new card    // Not matched during migration
}
```

**No runtime searching** - migration handles all matching upfront!

## Troubleshooting

### Migration doesn't find any cards

**Causes:**
- Planka credentials not in `.env`
- Discord token not in `.env`
- No cards exist in Planka
- Card names don't match thread names

**Solution:**
```bash
# Check .env file
cat .env | grep PLANKA
cat .env | grep DISCORD

# Verify Planka access
curl -X POST $PLANKA_URL/api/access-tokens \
  -H "Content-Type: application/json" \
  -d '{"emailOrUsername":"...","password":"..."}'

# Check card names in Planka web UI
```

### Some threads not matched

**Cause:** Card names don't exactly match thread names

**Solution:**
1. Check migration output for `[NOT FOUND]` entries
2. Compare thread names with Planka card names
3. Either:
   - Rename cards in Planka to match exactly
   - OR let bot create new cards

### Migration  authentication errors

**Cause:** Invalid Planka credentials

**Solution:**
```bash
# Test credentials manually
curl -X POST https://your-planka.com/api/access-tokens \
  -H "Content-Type: application/json" \
  -d '{"emailOrUsername":"bot@example.com","password":"yourpass"}'
```

### Want to re-run matching

**Solution:**
```bash
# Clear card IDs in database
sqlite3 data/tasks.db "UPDATE tasks SET planka_card_id = NULL, planka_list_id = NULL"

# Re-run migration
./migrate
```

## Best Practices

### 1. Test First

```bash
# Backup database
cp data/tasks.db data/tasks.db.backup

# Run migration
./migrate

# If something wrong, restore
mv data/tasks.db.backup data/tasks.db
```

### 2. Consistent Naming

- Copy exact thread names when creating Planka cards
- Avoid manual typing (introduces errors)
- Use copy-paste for accuracy

### 3. Verify Results

```bash
# Check matched threads
sqlite3 data/tasks.db "SELECT COUNT(*) FROM tasks WHERE planka_card_id IS NOT NULL"

# Check unmatched threads
sqlite3 data/tasks.db "SELECT COUNT(*) FROM tasks WHERE planka_card_id IS NULL"

# See specific matches
sqlite3 data/tasks.db "SELECT thread_id, planka_card_id FROM tasks WHERE planka_card_id IS NOT NULL"
```

### 4. Monitor Bot Logs

After migration, watch for:
- "Using existing card" - Good! Migration worked
- "Creating new card" - Thread wasn't matched

## Migration Checklist

Before running migration:
- [ ] `.env` configured with all credentials
- [ ] `migration.json` created with mappings
- [ ] Cards created in Planka (if pre-creating)
- [ ] Database backed up

During migration:
- [ ] Watch for authentication success
- [ ] Note match success rate
- [ ] Check for errors

After migration:
- [ ] Verify match counts in database
- [ ] Start bot and test linked thread
- [ ] Test unlinked thread (should create new card)
- [ ] Monitor bot logs for issues

## Summary

**Key Points:**
- ✅ Matching happens during migration, not runtime
- ✅ Migration tool searches all Planka boards
- ✅ Exact name matching (case-sensitive)
- ✅ Unmatched threads create new cards automatically
- ✅ Bot runtime is simpler and faster
- ✅ Easy to retry/re-run migration

**Benefits over Runtime Matching:**
- Faster bot (no API searches during thread creation)
- Simpler bot code (just check database)
- Clear visibility (migration output shows all matches)
- Easier debugging (all matching in one place)
- Better control (run migration when you want)
