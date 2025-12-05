package obfuscation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sync"
)

// Obfuscator handles data obfuscation for sensitive labels
type Obfuscator struct {
	instanceMap  map[string]string            // original -> obfuscated
	jobMap       map[string]string            // original -> obfuscated
	customLabels map[string]map[string]string // label -> (original -> obfuscated)
	mu           sync.RWMutex

	instanceCounter int            // counter for generating IPs
	jobCounter      map[string]int // counter per component
	customCounters  map[string]int // counter per custom label type
}

// NewObfuscator creates a new obfuscator
func NewObfuscator() *Obfuscator {
	return &Obfuscator{
		instanceMap:    make(map[string]string),
		jobMap:         make(map[string]string),
		customLabels:   make(map[string]map[string]string),
		jobCounter:     make(map[string]int),
		customCounters: make(map[string]int),
	}
}

// ObfuscateInstance obfuscates instance label (IP:PORT)
// Uses obviously fake IP pool (777.777.x.x) to make obfuscation clear
func (o *Obfuscator) ObfuscateInstance(instance string) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Check cache
	if obf, exists := o.instanceMap[instance]; exists {
		return obf
	}

	// Parse host and port
	_, port, err := net.SplitHostPort(instance)
	if err != nil {
		// If cannot parse, use simple hash
		obfuscated := o.hashString(instance)
		o.instanceMap[instance] = obfuscated
		return obfuscated
	}

	// Generate obfuscated IP from 777.777.x.x pool (obviously fake)
	o.instanceCounter++
	// Use modulo to cycle through 777.777.1.1-777.777.255.255
	thirdOctet := ((o.instanceCounter - 1) / 255) + 1
	fourthOctet := ((o.instanceCounter - 1) % 255) + 1
	newIP := fmt.Sprintf("777.777.%d.%d", thirdOctet, fourthOctet)

	// Reconstruct with original port
	obfuscated := net.JoinHostPort(newIP, port)
	o.instanceMap[instance] = obfuscated

	return obfuscated
}

// ObfuscateJob obfuscates job name
// Format: original job -> vm_component_<component>_<N>
func (o *Obfuscator) ObfuscateJob(job string, component string) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Check cache
	if obf, exists := o.jobMap[job]; exists {
		return obf
	}

	// Increment counter for this component
	o.jobCounter[component]++
	obfuscated := fmt.Sprintf("vm_component_%s_%d", component, o.jobCounter[component])

	o.jobMap[job] = obfuscated
	return obfuscated
}

// ObfuscateCustomLabel obfuscates custom labels (pod, namespace, etc.)
// Format: <label-type>-<N> (e.g., "pod-1", "namespace-1")
func (o *Obfuscator) ObfuscateCustomLabel(labelName, value string) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Initialize map for this label type if needed
	if o.customLabels[labelName] == nil {
		o.customLabels[labelName] = make(map[string]string)
	}

	// Check cache
	if obf, exists := o.customLabels[labelName][value]; exists {
		return obf
	}

	// Increment counter for this label type
	o.customCounters[labelName]++
	obfuscated := fmt.Sprintf("%s-%d", labelName, o.customCounters[labelName])

	o.customLabels[labelName][value] = obfuscated
	return obfuscated
}

// GetMappings returns copies of obfuscation mappings
// Returns instanceMap (original->obfuscated) and jobMap (original->obfuscated)
func (o *Obfuscator) GetMappings() (instanceMap, jobMap map[string]string) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	// Create copies to prevent external modification
	instanceMap = make(map[string]string, len(o.instanceMap))
	for k, v := range o.instanceMap {
		instanceMap[k] = v
	}

	jobMap = make(map[string]string, len(o.jobMap))
	for k, v := range o.jobMap {
		jobMap[k] = v
	}

	return instanceMap, jobMap
}

// hashString creates a deterministic hash of a string
// Used as fallback when standard obfuscation cannot be applied
func (o *Obfuscator) hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8]) // Use first 8 bytes (16 hex chars)
}
