package hashing

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

const (
	DefaultvirtualNodes = 150
)

type HashRing struct {
	ring            []uint32
	positionToNode  map[uint32]string
	nodeToPositions map[string][]uint32
	virtualNodes    int

	mu sync.RWMutex
}

func NewHashRing(virtualNodes int) *HashRing {
	if virtualNodes <= 0 {
		virtualNodes = DefaultvirtualNodes
	}

	return &HashRing{
		ring:            make([]uint32, 0),
		positionToNode:  make(map[uint32]string),
		nodeToPositions: make(map[string][]uint32),
		virtualNodes:    virtualNodes,
	}
}

func hash(key string) uint32 {
	h := sha256.Sum256([]byte(key))
	return binary.BigEndian.Uint32(h[:4])
}

func (hr *HashRing) AddNode(nodeName string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	if _, exists := hr.nodeToPositions[nodeName]; exists {
		return
	}
	positions := make([]uint32, 0, hr.virtualNodes)
	for i := 0; i < hr.virtualNodes; i++ {
		virtualkey := fmt.Sprintf("%s#%d", nodeName, i)
		position := hash(virtualkey)

		for hr.positionToNode[position] != "" {
			position++
		}

		positions = append(positions, position)
		hr.positionToNode[position] = nodeName
		hr.ring = append(hr.ring, position)
	}

	hr.nodeToPositions[nodeName] = positions

	sort.Slice(hr.ring, func(i, j int) bool {
		return hr.ring[i] < hr.ring[j]
	})

}

func (hr *HashRing) RemoveNode(nodeName string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	positions, exists := hr.nodeToPositions[nodeName]
	if !exists {
		return
	}

	// remove all virtuak positons
	for _, pos := range positions {
		delete(hr.positionToNode, pos)

	}

	delete(hr.nodeToPositions, nodeName)

	// rebuild ring without removed positons

	newRing := make([]uint32, 0, len(hr.ring)-len(positions))
	posset := make(map[uint32]bool)
	for _, p := range positions {
		posset[p] = true
	}

	for _, pos := range hr.ring {
		if !posset[pos] {
			newRing = append(newRing, pos)
		}
	}
	hr.ring = newRing

}

// GetNode returns the node responsible for a given key
// Returns empty string if ring is empty
func (hr *HashRing) GetNode(key string) string {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	if len(hr.ring) == 0 {
		return ""
	}

	position := hash(key)
	return hr.getNodeForPosition(position)
}

// getNodeForPosition finds the node responsible for a position
// Uses binary search to find first node clockwise from position
func (hr *HashRing) getNodeForPosition(position uint32) string {
	// Binary search for first position >= our position
	idx := sort.Search(len(hr.ring), func(i int) bool {
		return hr.ring[i] >= position
	})

	// If we went past the end, wrap around to first node
	if idx >= len(hr.ring) {
		idx = 0
	}

	return hr.positionToNode[hr.ring[idx]]
}

func (hr *HashRing) GetNodes(key string, count int) []string {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	if len(hr.ring) == 0 {
		return nil
	}

	uniqueNodes := len(hr.nodeToPositions)
	if count > uniqueNodes {
		count = uniqueNodes
	}

	position := hash(key)
	idx := sort.Search(len(hr.ring), func(i int) bool {
		return hr.ring[i] > position
	})

	if idx >= len(hr.ring) {
		idx = 0
	}

	result := make([]string, 0, count)
	seen := make(map[string]bool)
	for len(result) < count {
		node := hr.positionToNode[hr.ring[idx]]

		if !seen[node] {
			seen[node] = true
			result = append(result, node)
		}

		idx = (idx + 1) % len(hr.ring)
	}

	return result
}

func (hr *HashRing) GetAllNodes() []string {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	nodes := make([]string, 0, len(hr.nodeToPositions))
	for node, _ := range hr.nodeToPositions {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	return nodes
}

func (hr *HashRing) NodeCount() int {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	return len(hr.nodeToPositions)
}

func (hr *HashRing) IsEmpty() bool {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	return len(hr.ring) == 0
}

// GetNodeLoad returns how many virtual positions each node has
// Useful for debugging distribution
func (hr *HashRing) GetNodeLoad() map[string]int {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	load := make(map[string]int)
	for node, positions := range hr.nodeToPositions {
		load[node] = len(positions)
	}
	return load
}

func (hr *HashRing) DebugPrint() {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	fmt.Printf("\n=== Hash Ring State ===\n")
	fmt.Printf("Physical Nodes: %d\n", len(hr.nodeToPositions))
	fmt.Printf("Virtual Nodes: %d (per physical)\n", hr.virtualNodes)
	fmt.Printf("Total Ring Positions: %d\n", len(hr.ring))

	fmt.Printf("\nNode Distribution:\n")
	for node, positions := range hr.nodeToPositions {
		fmt.Printf("  %s: %d positions\n", node, len(positions))
	}
}
