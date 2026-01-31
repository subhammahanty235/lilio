package storagemodels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/subhammahanty235/lilio/pkg/storage"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// GDriveBackend implements StorageBackend for Google Drive
type GDriveBackend struct {
	name      string
	priority  int
	folderID  string
	service   *drive.Service
	tokenPath string
	credsPath string
	mu        sync.RWMutex

	// Cache chunk IDs to file IDs mapping
	chunkCache map[string]string

	// Stats
	chunksStored int64
	bytesStored  int64
}

// NewGDriveBackend creates a new Google Drive backend
func NewGDriveBackend(name, credentialsPath, tokenPath, folderID string, priority int) (*GDriveBackend, error) {
	ctx := context.Background()

	// Read credentials file
	credsData, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read credentials file: %w", err)
	}

	// Create OAuth2 config
	config, err := google.ConfigFromJSON(credsData, drive.DriveFileScope)
	if err != nil {
		return nil, fmt.Errorf("unable to parse credentials: %w", err)
	}

	// Get token (from file or new authorization)
	token, err := getToken(config, tokenPath)
	if err != nil {
		return nil, fmt.Errorf("unable to get token: %w", err)
	}

	// Create Drive service
	client := config.Client(ctx, token)
	service, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("unable to create Drive service: %w", err)
	}

	backend := &GDriveBackend{
		name:       name,
		priority:   priority,
		folderID:   folderID,
		service:    service,
		tokenPath:  tokenPath,
		credsPath:  credentialsPath,
		chunkCache: make(map[string]string),
	}

	// Create lilio folder if folderID not specified
	if folderID == "" {
		folder, err := backend.createOrGetFolder("lilio-storage")
		if err != nil {
			return nil, fmt.Errorf("failed to create storage folder: %w", err)
		}
		backend.folderID = folder.Id
		fmt.Printf("  Using Google Drive folder: %s (ID: %s)\n", folder.Name, folder.Id)
	}

	// Load existing chunks into cache
	backend.loadChunkCache()

	return backend, nil
}

// getToken retrieves token from file or initiates new authorization
func getToken(config *oauth2.Config, tokenPath string) (*oauth2.Token, error) {
	// Try to load from file
	token, err := tokenFromFile(tokenPath)
	if err == nil {
		return token, nil
	}

	// Get new token via authorization
	token, err = getTokenFromWeb(config)
	if err != nil {
		return nil, err
	}

	// Save token for future use
	saveToken(tokenPath, token)
	return token, nil
}

// getTokenFromWeb starts OAuth flow
func getTokenFromWeb(config *oauth2.Config) (*oauth2.Token, error) {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)

	fmt.Println("\n╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║           Google Drive Authorization Required              ║")
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Println("║  1. Open this URL in your browser:                         ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Printf("\n%s\n\n", authURL)
	fmt.Print("2. Enter the authorization code: ")

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		return nil, fmt.Errorf("unable to read authorization code: %w", err)
	}

	token, err := config.Exchange(context.Background(), authCode)
	if err != nil {
		return nil, fmt.Errorf("unable to exchange code for token: %w", err)
	}

	return token, nil
}

// tokenFromFile loads token from file
func tokenFromFile(path string) (*oauth2.Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	token := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(token)
	return token, err
}

// saveToken saves token to file
func saveToken(path string, token *oauth2.Token) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(token)
}

// createOrGetFolder creates or gets existing lilio folder
func (g *GDriveBackend) createOrGetFolder(name string) (*drive.File, error) {
	// Search for existing folder
	query := fmt.Sprintf("name='%s' and mimeType='application/vnd.google-apps.folder' and trashed=false", name)
	fileList, err := g.service.Files.List().Q(query).Fields("files(id, name)").Do()
	if err != nil {
		return nil, err
	}

	if len(fileList.Files) > 0 {
		return fileList.Files[0], nil
	}

	// Create new folder
	folder := &drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
	}

	return g.service.Files.Create(folder).Fields("id, name").Do()
}

// loadChunkCache loads existing chunk mappings
func (g *GDriveBackend) loadChunkCache() {
	query := fmt.Sprintf("'%s' in parents and trashed=false", g.folderID)

	err := g.service.Files.List().Q(query).Fields("files(id, name, size)").Pages(context.Background(),
		func(page *drive.FileList) error {
			for _, file := range page.Files {
				g.chunkCache[file.Name] = file.Id
				g.chunksStored++
				g.bytesStored += file.Size
			}
			return nil
		})

	if err != nil {
		fmt.Printf("Warning: failed to load chunk cache: %v\n", err)
	}
}

// ==================== StorageBackend Interface ====================

// Info returns backend metadata
func (g *GDriveBackend) Info() storage.BackendInfo {
	return storage.BackendInfo{
		Name:     g.name,
		Type:     storage.BackendTypeGDrive,
		Status:   storage.StatusOnline,
		Priority: g.priority,
	}
}

// Health checks if Google Drive is accessible
func (g *GDriveBackend) Health() error {
	_, err := g.service.About.Get().Fields("user").Do()
	if err != nil {
		return fmt.Errorf("google drive not accessible: %w", err)
	}
	return nil
}

// StoreChunk uploads a chunk to Google Drive
func (g *GDriveBackend) StoreChunk(chunkID string, data []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.chunkCache[chunkID]; exists {
		return nil
	}
	file := &drive.File{
		Name:    chunkID,
		Parents: []string{g.folderID},
	}

	reader := &byteReader{data: data}
	uploaded, err := g.service.Files.Create(file).Media(reader).Fields("id, size").Do()
	if err != nil {
		return fmt.Errorf("failed to upload chunk: %w", err)
	}
	g.chunkCache[chunkID] = uploaded.Id
	g.chunksStored++
	g.bytesStored += int64(len(data))

	return nil
}

// RetrieveChunk downloads a chunk from Google Drive
func (g *GDriveBackend) RetrieveChunk(chunkID string) ([]byte, error) {
	g.mu.RLock()
	fileID, exists := g.chunkCache[chunkID]
	g.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("chunk not found: %s", chunkID)
	}
	resp, err := g.service.Files.Get(fileID).Download()
	if err != nil {
		return nil, fmt.Errorf("failed to download chunk: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read chunk data: %w", err)
	}

	return data, nil
}

// DeleteChunk removes a chunk from Google Drive
func (g *GDriveBackend) DeleteChunk(chunkID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	fileID, exists := g.chunkCache[chunkID]
	if !exists {
		return nil
	}

	err := g.service.Files.Delete(fileID).Do()
	if err != nil {
		return fmt.Errorf("failed to delete chunk: %w", err)
	}

	delete(g.chunkCache, chunkID)
	g.chunksStored--

	return nil
}

// HasChunk checks if chunk exists
func (g *GDriveBackend) HasChunk(chunkID string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	_, exists := g.chunkCache[chunkID]
	return exists
}

// ListChunks returns all chunk IDs
func (g *GDriveBackend) ListChunks() ([]string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	chunks := make([]string, 0, len(g.chunkCache))
	for chunkID := range g.chunkCache {
		chunks = append(chunks, chunkID)
	}
	return chunks, nil
}

// Stats returns storage statistics
func (g *GDriveBackend) Stats() (storage.BackendStats, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	about, err := g.service.About.Get().Fields("storageQuota").Do()
	var bytesFree int64 = -1
	if err == nil && about.StorageQuota != nil {
		limit := about.StorageQuota.Limit
		usage := about.StorageQuota.Usage
		if limit > 0 {
			bytesFree = limit - usage
		}
	}

	return storage.BackendStats{
		BytesUsed:    g.bytesStored,
		BytesFree:    bytesFree,
		ChunksStored: g.chunksStored,
		LastChecked:  time.Now(),
	}, nil
}

// byteReader implements io.Reader for byte slice
type byteReader struct {
	data   []byte
	offset int
}

func (r *byteReader) Read(p []byte) (n int, err error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

// Ensure http.Client compatibility
var _ io.Reader = (*byteReader)(nil)
