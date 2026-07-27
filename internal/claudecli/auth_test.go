package claudecli

import "testing"

func TestParseAuthenticationStatusJSON(t *testing.T) {
	t.Parallel()

	raw := `{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max"}`
	got, ok := ParseAuthenticationStatus(raw)
	if !ok {
		t.Fatalf("ParseAuthenticationStatus() reported !ok")
	}
	if !got.LoggedIn {
		t.Fatalf("LoggedIn = false, want true")
	}
	if got.AuthMethod != "claude.ai" {
		t.Fatalf("AuthMethod = %q, want claude.ai", got.AuthMethod)
	}
	if got.SubscriptionType != "max" {
		t.Fatalf("SubscriptionType = %q, want max", got.SubscriptionType)
	}
}

func TestParseAuthenticationStatusExtractsEmbeddedJSON(t *testing.T) {
	t.Parallel()

	raw := "warning: something\n{\"loggedIn\":false,\"authMethod\":\"api_key\"}\n"
	got, ok := ParseAuthenticationStatus(raw)
	if !ok {
		t.Fatalf("ParseAuthenticationStatus() reported !ok")
	}
	if got.LoggedIn {
		t.Fatalf("LoggedIn = true, want false")
	}
	if got.AuthMethod != "api_key" {
		t.Fatalf("AuthMethod = %q, want api_key", got.AuthMethod)
	}
}

func TestAuthenticationDetailIncludesMethodAndSubscription(t *testing.T) {
	t.Parallel()

	got := AuthenticationDetail(AuthenticationStatus{
		LoggedIn:         true,
		AuthMethod:       "claude.ai",
		SubscriptionType: "max",
	})
	want := "Claude Code ready via claude.ai (max)"
	if got != want {
		t.Fatalf("AuthenticationDetail() = %q, want %q", got, want)
	}
}
