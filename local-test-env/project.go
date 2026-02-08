package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func loadProjectName(path string) (string, bool, error) {
	if path == "" {
		return "", false, fmt.Errorf("project file path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return "", false, nil
	}
	return name, true, nil
}

func saveProjectName(path string, name string) error {
	if path == "" {
		return fmt.Errorf("project file path is empty")
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("project name is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(name)+"\n"), 0o644)
}

func getProjectFilePath() string {
	path := getEnvOrDefault("VMGATHER_PROJECT_FILE", ".compose-project")
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(".", path)
}

func getOrCreateProjectName(reset bool) (string, error) {
	projectFile := getProjectFilePath()
	if !reset {
		if existing, ok, err := loadProjectName(projectFile); err != nil {
			return "", err
		} else if ok {
			return existing, nil
		}
		return "", fmt.Errorf("project name file %q not found; run with 'reset' first", projectFile)
	}

	prefix := getEnvOrDefault("VMGATHER_PROJECT_PREFIX", "vmtest")
	name := generateProjectName(prefix)
	if err := saveProjectName(projectFile, name); err != nil {
		return "", err
	}
	return name, nil
}

func generateProjectName(prefix string) string {
	p := sanitizeComposeProject(prefix)
	if p == "" {
		p = "vmtest"
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%s_%d_%04d", p, time.Now().Unix(), r.Intn(10_000))
}

func sanitizeComposeProject(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "" {
		return ""
	}
	return out
}
