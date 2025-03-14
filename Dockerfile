# Use the official Golang image to build the Go application
FROM golang:1.24.1 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the Go module files
COPY go.mod go.sum ./

# Download the Go module dependencies
RUN go mod download

# Copy the rest of the application source code
COPY . .

# Build the Go application
RUN make

# Use a minimal base image to run the Go application
FROM debian:stable-slim

# Set the working directory inside the container
WORKDIR /app

# Copy the built Go application from the builder stage
COPY --from=builder /app/status-checker .
COPY --from=builder /app/static/index.html ./static/index.html

RUN chmod +x status-checker
RUN apt update && apt install --reinstall ca-certificates -y

# Expose the port the application runs on
EXPOSE 8081

# Command to run the Go application
CMD ["/app/status-checker"]