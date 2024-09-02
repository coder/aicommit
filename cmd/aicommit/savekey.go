package main

import (
	"errors"
	"os"
	"path/filepath"
)

func configDir() (string, error) {
	cdir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	err = os.MkdirAll(filepath.Join(cdir, "aicommit"), 0o700)
	if err != nil {
		return "", err
	}
	return filepath.Join(cdir, "aicommit"), nil
}

func keyPath() (string, error) {
	cdir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cdir, "openai.key"), nil
}

func saveKey(key string) error {
	if key == "" {
		return errors.New("key is empty")
	}
	kp, err := keyPath()
	if err != nil {
		return err
	}
	return os.WriteFile(kp, []byte(key), 0o600)
}

func loadKey() (string, error) {
	kp, err := keyPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(kp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
