package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"educoder-cli/internal/educoder"
)

type profileClient struct {
	fakeClient
	profile json.RawMessage
	path    string
}

func (p *profileClient) RecordedRequest(_ context.Context, method, path string, query url.Values, _ json.RawMessage) (*educoder.RecordedResponse, error) {
	p.path = path
	return &educoder.RecordedResponse{StatusCode: http.StatusOK, Body: p.profile}, nil
}

func TestAvatarResolverUsesOnlyOfficialUserImageKeys(t *testing.T) {
	for _, tc := range []struct {
		name, profile, want string
	}{
		{"official", `{"image_url":"https://data.educoder.net/images/avatars/User/profile9?t=1234"}`, "profile9"},
		{"path", `{"avatar_url":"/images/avatars/User/profile9"}`, "profile9"},
		{"external", `{"image_url":"https://foreign.example/images/avatars/User/other"}`, "42"},
		{"wrong family", `{"image_url":"https://data.educoder.net/images/avatars/LaboratorySetting/1login"}`, "42"},
		{"empty", `{}`, "42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &profileClient{profile: json.RawMessage(tc.profile)}
			key := resolveAvatarKey(context.Background(), client, educoder.User{UserID: 42})
			if key != tc.want || client.path != "/api/users/get_user_info.json" {
				t.Fatalf("avatar key=%q path=%q, want %q", key, client.path, tc.want)
			}
		})
	}
	if key := resolveAvatarKey(context.Background(), &fakeClient{}, educoder.User{}); key != "b" {
		t.Fatalf("missing user identity fallback = %q", key)
	}
}
