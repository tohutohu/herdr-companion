package main

import "testing"

func TestFirebase設定は同じプロジェクトのAndroidアプリだけを取り込む(t *testing.T) {
	key := []byte(`{"type":"service_account","project_id":"test"}`)
	android := []byte(`{"project_info":{"project_id":"test","project_number":"123"},"client":[{"client_info":{"mobilesdk_app_id":"1:123:android:abc","android_client_info":{"package_name":"com.tohutohu.herdrmobile"}},"api_key":[{"current_key":"client-api-key"}]}]}`)
	o, err := firebaseOptions(key, android)
	if err != nil || o.SenderID != "123" || o.APIKey != "client-api-key" {
		t.Fatal(o, err)
	}
	for _, invalid := range []string{`{}`, `{"type":"service_account","project_id":"other"}`, `{"type":"service_account","project_id":"test","token_uri":"https://evil.example"}`} {
		if _, err := firebaseOptions([]byte(invalid), android); err == nil {
			t.Fatal("invalid service account accepted")
		}
	}
}
