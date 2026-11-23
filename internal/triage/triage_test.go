package triage

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCorrelatesPortAndKeepsLatestRepeatedEvidence(t *testing.T) {
	report, err := Analyze(Input{
		Symptom: "Health check fails on port 8080",
		Config:  "\n# example listen 8080;\nlisten [::]:9090;",
		Logs:    "\nfailure: connection refused\nfailure: connection refused",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
	port, refused := report.Findings[0], report.Findings[1]
	if port.Title != "Possible listen port mismatch" || port.Evidence[0].Line != 3 {
		t.Fatalf("port finding: %+v", port)
	}
	if refused.Matches != 2 || len(refused.Evidence) != 1 || refused.Evidence[0].Line != 3 {
		t.Fatalf("refused finding: %+v", refused)
	}
}

func TestDoesNotFlagMatchingOrAmbiguousPort(t *testing.T) {
	for _, input := range []Input{
		{Symptom: "port 8080", Config: "listen 9090;\nlisten 8080;"},
		{Symptom: "port 8080 or port 9090", Config: "listen 3000;"},
		{Symptom: "port 99999", Config: "listen 3000;"},
	} {
		report, err := Analyze(input)
		if err != nil || len(report.Findings) != 0 {
			t.Fatalf("Analyze(%+v) = %+v, %v", input, report, err)
		}
	}
}

func TestMasksSecretsInEvidenceAndBrief(t *testing.T) {
	report, err := Analyze(Input{
		Symptom: "Checkout failed with access_token=local-secret",
		Logs:    "Authorization: Bearer header-secret\nCookie: id=session-secret; other=another-secret\nconnection refused to postgres://build:database-secret@db:5432",
		Config:  "DB_PASSWORD=\"pass with spaces\"\n-----BEGIN PRIVATE KEY-----\nraw-key\n-----END PRIVATE KEY-----\nPORT=8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Redactions != 6 {
		t.Fatalf("redactions = %d", report.Redactions)
	}
	content := report.Brief + report.Findings[0].Evidence[0].Text
	for _, secret := range []string{"local-secret", "header-secret", "session-secret", "another-secret", "database-secret", "pass with spaces", "raw-key"} {
		if strings.Contains(content, secret) {
			t.Errorf("secret %q appeared in report", secret)
		}
	}
	if !strings.Contains(report.Brief, "5 | PORT=8080") {
		t.Fatal("redaction shifted config line numbers")
	}
}

func TestLimitsAndNoMatchBrief(t *testing.T) {
	for _, input := range []Input{
		{},
		{Logs: strings.Repeat("é", MaxArtifactBytes/2+1)},
		{Logs: "binary\x00data"},
		{Symptom: strings.Repeat("a", MaxSymptomBytes+1), Logs: "info"},
	} {
		if _, err := Analyze(input); err == nil {
			t.Errorf("accepted invalid input of %d log bytes", len(input.Logs))
		}
	}
	report, err := Analyze(Input{Logs: "Running " + strings.Repeat("✓", 200)})
	if err != nil || len(report.Findings) != 0 || !strings.Contains(report.Brief, "> 1 | Running") || !utf8.ValidString(report.Brief) {
		t.Fatalf("no-match report: %+v, %v", report, err)
	}
}
