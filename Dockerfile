FROM golang:1.22.5-bookworm
ARG DISCORD_TOKEN
ARG DISCORD_CHANNEL
ARG TAIGA_URL
ARG TAIGA_USERNAME 
ARG TAIGA_PASSWORD
ARG TAIGA_PROJECT_ID
ARG TAIGA_BACKLOG
ARG TAIGA_IN_PROGRESS
ARG TAIGA_COMPLETE

# Set the working directory
WORKDIR /app

COPY . .
RUN apt-get update && \
    apt-get -y install build-essential

RUN CGO_ENABLED=1 go build -o taiga_bridge

# Set the library path and Tesseract data directory environment variables
ENV LD_LIBRARY_PATH=/usr/local/lib:/usr/lib:/usr/lib/x86_64-linux-gnu
ENV DISCORD_TOKEN=$DISCORD_TOKEN
ENV DISCORD_CHANNEL=$DISCORD_CHANNEL
ENV TAIGA_URL=$TAIGA_URL
ENV TAIGA_USERNAME=$TAIGA_USERNAME
ENV TAIGA_PASSWORD=$TAIGA_PASSWORD
ENV TAIGA_PROJECT_ID=$TAIGA_PROJECT_ID
ENV TAIGA_BACKLOG=$TAIGA_BACKLOG
ENV TAIGA_IN_PROGRESS=$TAIGA_IN_PROGRESS
ENV TAIGA_COMPLETE=$TAIGA_COMPLETE


# Command to run the application
CMD ["/app/taiga_bridge"]

