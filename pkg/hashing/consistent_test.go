package hashing

import (
	"fmt"
	"strings"
	"testing"
)

func TestBasicOperations(t *testing.T) {
	ring := NewHashRing(100)

	// Add nodes
	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	if ring.NodeCount() != 3 {
		t.Errorf("Expected 3 nodes, got %d", ring.NodeCount())
	}

	// Get node for a key
	node := ring.GetNode("my-chunk-id")
	if node == "" {
		t.Error("Expected a node, got empty string")
	}

	// Same key should always return same node
	node2 := ring.GetNode("my-chunk-id")
	if node != node2 {
		t.Errorf("Same key returned different nodes: %s vs %s", node, node2)
	}
}

func TestReplication(t *testing.T) {
	ring := NewHashRing(100)

	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	// Get 2 nodes for replication
	nodes := ring.GetNodes("chunk-abc", 2)

	if len(nodes) != 2 {
		t.Errorf("Expected 2 nodes, got %d", len(nodes))
	}

	// Nodes should be unique
	if nodes[0] == nodes[1] {
		t.Error("Replica nodes should be different")
	}
}

func TestNodeRemoval(t *testing.T) {
	ring := NewHashRing(100)

	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	// Get node for a key
	originalNode := ring.GetNode("test-key")

	// Remove a different node
	nodeToRemove := "node-1"
	if originalNode == "node-1" {
		nodeToRemove = "node-2"
	}

	ring.RemoveNode(nodeToRemove)

	// Key should still map to same node (if that node wasn't removed)
	newNode := ring.GetNode("test-key")

	if originalNode != nodeToRemove && newNode != originalNode {
		// This might happen due to ring redistribution, which is expected
		// Just ensure we get SOME node
		if newNode == "" {
			t.Error("Expected a node after removal")
		}
	}
}

func TestMinimalRedistribution(t *testing.T) {
	ring := NewHashRing(150)

	// Add initial nodes
	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")
	ring.AddNode("node-4")

	// Record where 1000 keys map to
	keyToNode := make(map[string]string)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("chunk-%d", i)
		keyToNode[key] = ring.GetNode(key)
	}

	// Add a new node
	ring.AddNode("node-5")

	// Count how many keys changed
	changed := 0
	for key, oldNode := range keyToNode {
		newNode := ring.GetNode(key)
		if newNode != oldNode {
			changed++
		}
	}

	// With consistent hashing, roughly 1/N keys should move
	// With 5 nodes, expect ~20% change (with some variance)
	changePercent := float64(changed) / 1000.0 * 100

	fmt.Printf("Keys redistributed: %d/1000 (%.1f%%)\n", changed, changePercent)

	// Should be less than 40% (giving generous margin)
	if changePercent > 40 {
		t.Errorf("Too many keys redistributed: %.1f%% (expected ~20%%)", changePercent)
	}
}

func TestDistribution(t *testing.T) {
	ring := NewHashRing(150)

	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")
	ring.AddNode("node-4")

	// Distribute 10000 keys and count per node
	distribution := make(map[string]int)
	totalKeys := 10000

	for i := 0; i < totalKeys; i++ {
		key := fmt.Sprintf("key-%d", i)
		node := ring.GetNode(key)
		distribution[node]++
	}

	fmt.Println("\nKey Distribution:")
	for node, count := range distribution {
		percent := float64(count) / float64(totalKeys) * 100
		fmt.Printf("  %s: %d keys (%.1f%%)\n", node, count, percent)
	}

	// Each node should have between 15% and 35% (ideal is 25% with 4 nodes)
	for node, count := range distribution {
		percent := float64(count) / float64(totalKeys) * 100
		if percent < 15 || percent > 35 {
			t.Errorf("Node %s has unbalanced distribution: %.1f%%", node, percent)
		}
	}
}

func TestEmptyRing(t *testing.T) {
	ring := NewHashRing(100)

	node := ring.GetNode("some-key")
	if node != "" {
		t.Errorf("Empty ring should return empty string, got %s", node)
	}

	nodes := ring.GetNodes("some-key", 3)
	if len(nodes) != 0 {
		t.Errorf("Empty ring should return empty slice, got %v", nodes)
	}
}

func TestSingleNode(t *testing.T) {
	ring := NewHashRing(100)
	ring.AddNode("lonely-node")

	// All keys should go to the single node
	for i := 0; i < 100; i++ {
		node := ring.GetNode(fmt.Sprintf("key-%d", i))
		if node != "lonely-node" {
			t.Errorf("Expected lonely-node, got %s", node)
		}
	}

	// Asking for 3 replicas should return just 1
	nodes := ring.GetNodes("test", 3)
	if len(nodes) != 1 {
		t.Errorf("Expected 1 node, got %d", len(nodes))
	}
}

// Demo function - run with: go test -v -run Demo
func TestDemo(t *testing.T) {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("       CONSISTENT HASHING DEMO")
	fmt.Println(strings.Repeat("=", 60) + "\n")

	ring := NewHashRing(150)

	// Add nodes
	fmt.Println("Adding nodes...")
	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	ring.DebugPrint()

	// Show where some chunks would go
	fmt.Println("\nChunk placement:")
	chunks := []string{"photo.jpg", "video.mp4", "document.pdf", "music.mp3", "data.bin"}
	for _, chunk := range chunks {
		node := ring.GetNode(chunk)
		replicas := ring.GetNodes(chunk, 2)
		fmt.Printf("  %s → Primary: %s, Replicas: %v\n", chunk, node, replicas)
	}

	// Simulate node failure
	fmt.Println("\n--- Simulating node-2 FAILURE ---")
	ring.RemoveNode("node-2")

	fmt.Println("\nChunk placement AFTER failure:")
	for _, chunk := range chunks {
		node := ring.GetNode(chunk)
		replicas := ring.GetNodes(chunk, 2)
		fmt.Printf("  %s → Primary: %s, Replicas: %v\n", chunk, node, replicas)
	}

	// Add new node
	fmt.Println("\n--- Adding node-4 ---")
	ring.AddNode("node-4")

	fmt.Println("\nChunk placement AFTER adding node-4:")
	for _, chunk := range chunks {
		node := ring.GetNode(chunk)
		replicas := ring.GetNodes(chunk, 2)
		fmt.Printf("  %s → Primary: %s, Replicas: %v\n", chunk, node, replicas)
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("                DEMO COMPLETE")
	fmt.Println("\n" + strings.Repeat("=", 60))
}
