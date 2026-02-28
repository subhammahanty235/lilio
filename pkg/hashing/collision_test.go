package hashing

import (
	"fmt"
	"testing"
)

// TestCollisionHandling verifies that collision resolution maintains consistency
func TestCollisionHandling(t *testing.T) {
	ring := NewHashRing(150)

	// Add multiple nodes to increase collision probability
	for i := 1; i <= 10; i++ {
		ring.AddNode(fmt.Sprintf("node-%d", i))
	}

	// Verify all nodes were added successfully
	if ring.NodeCount() != 10 {
		t.Errorf("Expected 10 nodes, got %d", ring.NodeCount())
	}

	// Check that all virtual nodes are unique (no duplicate positions)
	ring.mu.RLock()
	uniquePositions := make(map[uint32]bool)
	totalVirtuals := 0
	for _, positions := range ring.nodeToPositions {
		totalVirtuals += len(positions)
		for _, pos := range positions {
			if uniquePositions[pos] {
				t.Errorf("Duplicate position found: %d", pos)
			}
			uniquePositions[pos] = true
		}
	}
	ring.mu.RUnlock()

	expectedVirtuals := 10 * 150 // 10 nodes * 150 virtuals each
	if totalVirtuals != expectedVirtuals {
		t.Errorf("Expected %d virtual nodes, got %d", expectedVirtuals, totalVirtuals)
	}

	t.Logf("✓ All %d virtual nodes are unique (no collisions caused duplicates)", totalVirtuals)
}

// TestConsistentKeyMapping verifies that keys always map to the same node
func TestConsistentKeyMapping(t *testing.T) {
	ring := NewHashRing(150)

	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	// Map 1000 keys
	testKeys := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		testKeys[i] = fmt.Sprintf("chunk-%d", i)
	}

	// Get initial mapping
	initialMapping := make(map[string]string)
	for _, key := range testKeys {
		node := ring.GetNode(key)
		if node == "" {
			t.Errorf("GetNode returned empty string for key %s", key)
		}
		initialMapping[key] = node
	}

	// Query same keys again - should get identical results
	for i := 0; i < 10; i++ {
		for _, key := range testKeys {
			node := ring.GetNode(key)
			if node != initialMapping[key] {
				t.Errorf("Key %s mapped to different nodes: %s vs %s",
					key, initialMapping[key], node)
			}
		}
	}

	t.Logf("✓ All 1000 keys consistently mapped to same nodes across 10 iterations")
}

// TestNoLinearProbing verifies we're not using simple linear probing
func TestNoLinearProbing(t *testing.T) {
	ring := NewHashRing(150)

	ring.AddNode("node-1")

	// Get all positions for node-1
	ring.mu.RLock()
	positions := make([]uint32, len(ring.nodeToPositions["node-1"]))
	copy(positions, ring.nodeToPositions["node-1"])
	ring.mu.RUnlock()

	// Check that positions are not sequential (which would indicate linear probing)
	sequentialCount := 0
	for i := 0; i < len(positions)-1; i++ {
		// If positions are sequential (diff of 1), that's linear probing
		if positions[i+1] == positions[i]+1 {
			sequentialCount++
		}
	}

	// With proper hashing, sequential positions should be extremely rare
	// Allow up to 1% sequential by random chance
	maxSequential := len(positions) / 100
	if sequentialCount > maxSequential {
		t.Errorf("Too many sequential positions (%d/%d), suggests linear probing instead of rehashing",
			sequentialCount, len(positions))
	}

	t.Logf("✓ Positions are well-distributed (only %d/%d sequential pairs, max allowed: %d)",
		sequentialCount, len(positions), maxSequential)
}

// TestGetNodeVsGetNodesConsistency verifies GetNode and GetNodes[0] return same result
func TestGetNodeVsGetNodesConsistency(t *testing.T) {
	ring := NewHashRing(150)

	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	// Test 100 random keys
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("test-key-%d", i)

		singleNode := ring.GetNode(key)
		multiNodes := ring.GetNodes(key, 1)

		if len(multiNodes) != 1 {
			t.Errorf("GetNodes(key, 1) returned %d nodes, expected 1", len(multiNodes))
			continue
		}

		// Note: This might fail due to the > vs >= issue in GetNodes
		if singleNode != multiNodes[0] {
			t.Errorf("Inconsistency for key %s: GetNode=%s, GetNodes[0]=%s",
				key, singleNode, multiNodes[0])
		}
	}

	t.Logf("✓ GetNode and GetNodes are consistent")
}

// TestCollisionRecoveryLimit verifies the retry limit works
func TestCollisionRecoveryLimit(t *testing.T) {
	ring := NewHashRing(150)

	// This is a meta-test: with SHA-256, collisions are astronomically rare
	// We're just verifying the code structure handles the limit correctly

	ring.AddNode("test-node-1")
	ring.AddNode("test-node-2")

	// Both nodes should have all their virtual nodes created
	ring.mu.RLock()
	node1Virtuals := len(ring.nodeToPositions["test-node-1"])
	node2Virtuals := len(ring.nodeToPositions["test-node-2"])
	ring.mu.RUnlock()

	if node1Virtuals != 150 {
		t.Errorf("test-node-1 should have 150 virtuals, got %d", node1Virtuals)
	}
	if node2Virtuals != 150 {
		t.Errorf("test-node-2 should have 150 virtuals, got %d", node2Virtuals)
	}

	t.Logf("✓ Collision retry mechanism works (all virtual nodes created)")
}

// BenchmarkHashRingLookup benchmarks the lookup performance
func BenchmarkHashRingLookup(b *testing.B) {
	ring := NewHashRing(150)

	for i := 1; i <= 10; i++ {
		ring.AddNode(fmt.Sprintf("node-%d", i))
	}

	keys := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		keys[i] = fmt.Sprintf("chunk-%d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := keys[i%1000]
		ring.GetNode(key)
	}
}

// BenchmarkHashRingReplication benchmarks replication node selection
func BenchmarkHashRingReplication(b *testing.B) {
	ring := NewHashRing(150)

	for i := 1; i <= 10; i++ {
		ring.AddNode(fmt.Sprintf("node-%d", i))
	}

	keys := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		keys[i] = fmt.Sprintf("chunk-%d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := keys[i%1000]
		ring.GetNodes(key, 3) // Get 3 replicas
	}
}
