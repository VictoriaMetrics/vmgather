package domain

import (
	"encoding/json"
	"testing"
	"time"
)

// TestAuthType_String tests AuthType constants
func TestAuthType_String(t *testing.T) {
	tests := []struct {
		name string
		auth AuthType
		want string
	}{
		{"none", AuthTypeNone, "none"},
		{"basic", AuthTypeBasic, "basic"},
		{"bearer", AuthTypeBearer, "bearer"},
		{"header", AuthTypeHeader, "header"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.auth) != tt.want {
				t.Errorf("AuthType = %v, want %v", tt.auth, tt.want)
			}
		})
	}
}

// TestVMConnection_JSONSerialization tests JSON marshaling/unmarshaling
func TestVMConnection_JSONSerialization(t *testing.T) {
	conn := VMConnection{
		URL: "http://vmselect:8481/select/0/prometheus",
		Auth: AuthConfig{
			Type:     AuthTypeBasic,
			Username: "user",
			Password: "pass",
		},
		SkipTLSVerify: true,
	}

	// Marshal to JSON
	data, err := json.Marshal(conn)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Unmarshal back
	var decoded VMConnection
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Verify
	if decoded.URL != conn.URL {
		t.Errorf("URL = %v, want %v", decoded.URL, conn.URL)
	}
	if decoded.Auth.Type != conn.Auth.Type {
		t.Errorf("AuthType = %v, want %v", decoded.Auth.Type, conn.Auth.Type)
	}
	if decoded.Auth.Username != conn.Auth.Username {
		t.Errorf("Username = %v, want %v", decoded.Auth.Username, conn.Auth.Username)
	}
}

// TestTimeRange_Duration tests time range duration calculation
func TestTimeRange_Duration(t *testing.T) {
	start := time.Date(2025, 11, 11, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	tr := TimeRange{
		Start: start,
		End:   end,
	}

	duration := tr.End.Sub(tr.Start)
	expectedDuration := 24 * time.Hour

	if duration != expectedDuration {
		t.Errorf("Duration = %v, want %v", duration, expectedDuration)
	}
}
