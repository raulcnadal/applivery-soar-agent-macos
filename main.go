//go:build !windows
// +build !windows

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.Println("Starting Applivery SOAR Agent for macOS...")

	stopChan := make(chan struct{})

	go runAgentLoop(stopChan)

	// Block until termination signal is received
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down Applivery SOAR Agent...")
	close(stopChan)
}