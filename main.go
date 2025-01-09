package main;
import dotenv "github.com/joho/godotenv"
import "github.com/bwmarrin/discordgo"
import "os"
import "fmt"
import "os/signal"

func main() {
  dotenv.Load();
  
  discord, err := discordgo.New("Bot " + os.Getenv("DISCORD_TOKEN"));
  if err != nil {
    panic(err);
  }
  discord.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		fmt.Println("Bot is ready")
	})
  // check in channel for new thread
  discord.AddHandler(createThread);
  discord.Identify.Intents = discordgo.IntentsGuildMessages;
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
  fmt.Println("Got a new thread in channel: " + t.ChannelID);
  thread := t.ChannelID;
  channel, err := s.Channel(thread);
  if err != nil {
    fmt.Println("Error getting channel: " + err.Error());
    return;
  }
  if (channel.ParentID != os.Getenv("DISCORD_CHANNEL_ID")) {
    return;
  }
  if (channel.MessageCount == 0) {

    //get thread description
    description := "";
    if (channel.Topic != "") {
      description = channel.Topic;
    }
    if (t.Thread.Topic != "") {
      description = t.Thread.Topic;
    }
    println(description);
    
    
  }
  println(channel.MessageCount);
  fmt.Println("Thread created: " + t.ID);
  
}

func createTask(title string, description string) {
  
}
