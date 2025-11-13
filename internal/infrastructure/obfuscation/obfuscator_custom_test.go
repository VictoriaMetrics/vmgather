package obfuscation

import (
	"testing"
)

func TestObfuscator_ObfuscateCustomLabel(t *testing.T) {
	obf := NewObfuscator()

	// Test pod obfuscation
	pod1 := obf.ObfuscateCustomLabel("pod", "vm-storage-zone-a-10")
	pod2 := obf.ObfuscateCustomLabel("pod", "vm-storage-zone-a-11")
	pod3 := obf.ObfuscateCustomLabel("pod", "vm-storage-zone-a-10") // Same as pod1

	// Should be deterministic
	if pod1 != "pod-1" {
		t.Errorf("Expected pod-1, got %s", pod1)
	}
	if pod2 != "pod-2" {
		t.Errorf("Expected pod-2, got %s", pod2)
	}
	if pod3 != pod1 {
		t.Errorf("Same pod should get same obfuscation: %s != %s", pod3, pod1)
	}
}

func TestObfuscator_ObfuscateCustomLabel_Namespace(t *testing.T) {
	obf := NewObfuscator()

	// Test namespace obfuscation
	ns1 := obf.ObfuscateCustomLabel("namespace", "ep-victoriametrics")
	ns2 := obf.ObfuscateCustomLabel("namespace", "kube-system")
	ns3 := obf.ObfuscateCustomLabel("namespace", "ep-victoriametrics") // Same as ns1

	if ns1 != "namespace-1" {
		t.Errorf("Expected namespace-1, got %s", ns1)
	}
	if ns2 != "namespace-2" {
		t.Errorf("Expected namespace-2, got %s", ns2)
	}
	if ns3 != ns1 {
		t.Errorf("Same namespace should get same obfuscation: %s != %s", ns3, ns1)
	}
}

func TestObfuscator_ObfuscateCustomLabel_Multiple(t *testing.T) {
	obf := NewObfuscator()

	// Test multiple label types
	pod1 := obf.ObfuscateCustomLabel("pod", "pod-a")
	ns1 := obf.ObfuscateCustomLabel("namespace", "ns-a")
	container1 := obf.ObfuscateCustomLabel("container", "cont-a")

	pod2 := obf.ObfuscateCustomLabel("pod", "pod-b")
	ns2 := obf.ObfuscateCustomLabel("namespace", "ns-b")

	// Each label type should have independent counter
	if pod1 != "pod-1" {
		t.Errorf("Expected pod-1, got %s", pod1)
	}
	if ns1 != "namespace-1" {
		t.Errorf("Expected namespace-1, got %s", ns1)
	}
	if container1 != "container-1" {
		t.Errorf("Expected container-1, got %s", container1)
	}
	if pod2 != "pod-2" {
		t.Errorf("Expected pod-2, got %s", pod2)
	}
	if ns2 != "namespace-2" {
		t.Errorf("Expected namespace-2, got %s", ns2)
	}
}

func TestObfuscator_ObfuscateCustomLabel_EmptyValue(t *testing.T) {
	obf := NewObfuscator()

	// Test empty value
	result := obf.ObfuscateCustomLabel("pod", "")
	if result != "pod-1" {
		t.Errorf("Expected pod-1 for empty value, got %s", result)
	}
}

func TestObfuscator_ObfuscateCustomLabel_Concurrent(t *testing.T) {
	obf := NewObfuscator()

	// Test concurrent access
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(n int) {
			for j := 0; j < 100; j++ {
				obf.ObfuscateCustomLabel("pod", "test-pod")
				obf.ObfuscateCustomLabel("namespace", "test-ns")
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have only one mapping for each
	pod := obf.ObfuscateCustomLabel("pod", "test-pod")
	ns := obf.ObfuscateCustomLabel("namespace", "test-ns")

	if pod != "pod-1" {
		t.Errorf("Expected pod-1, got %s", pod)
	}
	if ns != "namespace-1" {
		t.Errorf("Expected namespace-1, got %s", ns)
	}
}

func TestObfuscator_MixedObfuscation(t *testing.T) {
	obf := NewObfuscator()

	// Test mixing instance, job, and custom labels
	instance := obf.ObfuscateInstance("10.31.56.180:8482")
	job := obf.ObfuscateJob("vm-storage-zone-a", "vmstorage")
	pod := obf.ObfuscateCustomLabel("pod", "vm-storage-zone-a-10")
	namespace := obf.ObfuscateCustomLabel("namespace", "ep-victoriametrics")

	// All should work independently
	if instance != "777.777.1.1:8482" {
		t.Errorf("Expected 777.777.1.1:8482, got %s", instance)
	}
	if job != "vm_component_vmstorage_1" {
		t.Errorf("Expected vm_component_vmstorage_1, got %s", job)
	}
	if pod != "pod-1" {
		t.Errorf("Expected pod-1, got %s", pod)
	}
	if namespace != "namespace-1" {
		t.Errorf("Expected namespace-1, got %s", namespace)
	}
}

