package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notifications"
)

// Run only while the gateway using this config is stopped.
func importFirebaseCmd(args []string) error {
	fs := flag.NewFlagSet("import-firebase", flag.ContinueOnError)
	credentials := fs.String("service-account", "", "service-account JSON")
	android := fs.String("android-config", "", "google-services.json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *credentials == "" || *android == "" {
		return errors.New("select both the service-account JSON and google-services.json")
	}
	key, err := readSetupJSON(*credentials)
	if err != nil {
		return err
	}
	client, err := readSetupJSON(*android)
	if err != nil {
		return err
	}
	options, err := firebaseOptions(key, client)
	if err != nil {
		return err
	}
	if _, err := notifications.NewFCM(*credentials); err != nil {
		return errors.New("invalid Firebase service-account private key")
	}
	store, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	dir := filepath.Dir(store.Path())
	// A fresh private file avoids overwriting credentials still referenced by a config.
	f, err := os.CreateTemp(dir, "firebase-credentials-*.json")
	if err != nil {
		return err
	}
	path := f.Name()
	committed := false
	defer func() {
		if !committed {
			os.Remove(path)
		}
	}()
	if _, err = f.Write(key); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = store.Update(func(c *config.Config) { c.FCMCredentialsFile = path; c.FirebaseAndroid = options }); err != nil {
		return err
	}
	committed = true
	fmt.Println("Firebase configuration imported. Restart the gateway and pair Android.")
	return nil
}

func readSetupJSON(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("could not read selected configuration file")
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("configuration must be a JSON file smaller than 1 MiB")
	}
	return os.ReadFile(path)
}

func firebaseOptions(key, android []byte) (*config.FirebaseAndroid, error) {
	var sa struct {
		Type      string `json:"type"`
		ProjectID string `json:"project_id"`
		TokenURI  string `json:"token_uri"`
	}
	var doc struct {
		Project struct {
			ID     string `json:"project_id"`
			Number string `json:"project_number"`
		} `json:"project_info"`
		Clients []struct {
			Info struct {
				AppID   string `json:"mobilesdk_app_id"`
				Android struct {
					Package string `json:"package_name"`
				} `json:"android_client_info"`
			} `json:"client_info"`
			Keys []struct {
				Key string `json:"current_key"`
			} `json:"api_key"`
		} `json:"client"`
	}
	if json.Unmarshal(key, &sa) != nil || json.Unmarshal(android, &doc) != nil {
		return nil, errors.New("invalid Firebase JSON")
	}
	if sa.Type != "service_account" || sa.ProjectID == "" || sa.ProjectID != doc.Project.ID {
		return nil, errors.New("both files must belong to the same Firebase project")
	}
	if sa.TokenURI != "" && sa.TokenURI != "https://oauth2.googleapis.com/token" {
		return nil, errors.New("unsupported service-account token endpoint")
	}
	for _, c := range doc.Clients {
		if c.Info.Android.Package == "com.tohutohu.herdrmobile" && c.Info.AppID != "" && len(c.Keys) > 0 && c.Keys[0].Key != "" && doc.Project.Number != "" {
			return &config.FirebaseAndroid{APIKey: c.Keys[0].Key, ApplicationID: c.Info.AppID, ProjectID: doc.Project.ID, SenderID: doc.Project.Number}, nil
		}
	}
	return nil, errors.New("register an Android app with package com.tohutohu.herdrmobile and download google-services.json")
}
