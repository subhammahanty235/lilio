package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func handleWeb() {
	webCmd := flag.NewFlagSet("web", flag.ExitOnError)
	server := webCmd.String("server", defaultServer, "Server URL")
	webCmd.Parse(os.Args[2:])

	// Extract port from server URL
	port := "8080"
	if strings.Contains(*server, ":") {
		parts := strings.Split(*server, ":")
		if len(parts) > 1 {
			port = parts[len(parts)-1]
		}
	}

	// Health check - ping server
	fmt.Printf("Checking server at %s...\n", *server)

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get(*server + "/admin/stats")
	if err != nil {
		fmt.Printf("\n✗ Cannot connect to server at %s\n", *server)
		fmt.Println("\nServer is not running. Start it with:")
		fmt.Println("  lilio server")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("\n✗ Server returned status: %d\n", resp.StatusCode)
		fmt.Println("\nServer might not be ready. Please check the server status.")
		os.Exit(1)
	}

	// Server is running - construct UI URL
	uiURL := fmt.Sprintf("http://localhost:%s/ui", port)

	fmt.Printf("✓ Server is running at %s\n", *server)
	fmt.Printf("Opening web interface at %s\n\n", uiURL)

	// Open browser
	if err := openBrowser(uiURL); err != nil {
		fmt.Printf("Could not open browser automatically: %v\n", err)
		fmt.Printf("\nPlease open your browser to:\n%s\n", uiURL)
	}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}
