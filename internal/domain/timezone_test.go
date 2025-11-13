package domain

import (
	"testing"
	"time"
)

func TestTimezoneConversion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		timezone string
		wantErr  bool
	}{
		{
			name:     "UTC time",
			input:    "2025-01-15T10:00:00",
			timezone: "UTC",
			wantErr:  false,
		},
		{
			name:     "New York time",
			input:    "2025-01-15T10:00:00",
			timezone: "America/New_York",
			wantErr:  false,
		},
		{
			name:     "Tokyo time",
			input:    "2025-01-15T10:00:00",
			timezone: "Asia/Tokyo",
			wantErr:  false,
		},
		{
			name:     "London time",
			input:    "2025-01-15T10:00:00",
			timezone: "Europe/London",
			wantErr:  false,
		},
		{
			name:     "Invalid timezone",
			input:    "2025-01-15T10:00:00",
			timezone: "Invalid/Timezone",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse input time
			inputTime, err := time.Parse("2006-01-02T15:04:05", tt.input)
			if err != nil {
				t.Fatalf("Failed to parse input time: %v", err)
			}

			// Load timezone
			loc, err := time.LoadLocation(tt.timezone)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error for timezone %s, got nil", tt.timezone)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error loading timezone %s: %v", tt.timezone, err)
				return
			}

			// Convert to timezone
			converted := inputTime.In(loc)

			// Verify conversion worked
			if converted.Location().String() != loc.String() {
				t.Errorf("Timezone conversion failed: got %s, want %s", 
					converted.Location().String(), loc.String())
			}
		})
	}
}

func TestTimezoneOffsets(t *testing.T) {
	// Test that different timezones have different offsets
	baseTime := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	timezones := []string{
		"UTC",
		"America/New_York",
		"Europe/London",
		"Asia/Tokyo",
		"Australia/Sydney",
	}

	offsets := make(map[string]int)

	for _, tz := range timezones {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			t.Fatalf("Failed to load timezone %s: %v", tz, err)
		}

		converted := baseTime.In(loc)
		_, offset := converted.Zone()
		offsets[tz] = offset
	}

	// UTC should have offset 0
	if offsets["UTC"] != 0 {
		t.Errorf("UTC offset should be 0, got %d", offsets["UTC"])
	}

	// Other timezones should have different offsets
	if offsets["America/New_York"] == offsets["Asia/Tokyo"] {
		t.Error("New York and Tokyo should have different offsets")
	}
}

func TestTimeRangeWithTimezone(t *testing.T) {
	// Test that time range is correctly calculated with timezone
	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)

	timezones := []string{"UTC", "America/Los_Angeles", "Europe/Paris"}

	for _, tz := range timezones {
		t.Run(tz, func(t *testing.T) {
			loc, err := time.LoadLocation(tz)
			if err != nil {
				t.Fatalf("Failed to load timezone: %v", err)
			}

			nowInTZ := now.In(loc)
			oneHourAgoInTZ := oneHourAgo.In(loc)

			// Duration should be 1 hour regardless of timezone
			duration := nowInTZ.Sub(oneHourAgoInTZ)
			expectedDuration := 1 * time.Hour

			if duration != expectedDuration {
				t.Errorf("Duration mismatch in %s: got %v, want %v", 
					tz, duration, expectedDuration)
			}
		})
	}
}

func TestDateTimeLocalFormat(t *testing.T) {
	// Test that datetime-local format is correctly generated
	testTime := time.Date(2025, 1, 15, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		timezone string
		want     string
	}{
		{
			name:     "UTC",
			timezone: "UTC",
			want:     "2025-01-15T14:30",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.timezone)
			if err != nil {
				t.Fatalf("Failed to load timezone: %v", err)
			}

			converted := testTime.In(loc)
			formatted := converted.Format("2006-01-02T15:04")

			if formatted != tt.want {
				t.Errorf("Format mismatch: got %s, want %s", formatted, tt.want)
			}
		})
	}
}

