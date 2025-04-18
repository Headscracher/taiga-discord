FROM golang:1.22.5-bookworm

# Set the working directory
WORKDIR /app

COPY . .
RUN apt-get update && \
    apt-get -y install build-essential

RUN CGO_ENABLED=1 go build -o taiga_bridge

# Set the library path and Tesseract data directory environment variables
ENV LD_LIBRARY_PATH=/usr/local/lib:/usr/lib:/usr/lib/x86_64-linux-gnu


# Command to run the application
CMD ["/app/taiga_bridge"]

