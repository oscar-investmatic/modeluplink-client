package service

import (
	encodingxml "encoding/xml"
	"strings"
	"testing"
)

func TestWindowsTaskUsesOnlyInteractiveLeastPrivilegeLogon(t *testing.T) {
	document := windowsTaskXML("S-1-5-21-42", `C:\Users\Name & Co\helper.exe`, `_agent --config "C:\Users\Name & Co\config.json"`, true)
	var task any
	if err := encodingxml.Unmarshal([]byte(strings.Replace(document, "UTF-16", "UTF-8", 1)), &task); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<LogonType>InteractiveToken</LogonType>", "<RunLevel>LeastPrivilege</RunLevel>", "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>", "<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>", "Name &amp; Co", "<Enabled>true</Enabled>"} {
		if !strings.Contains(document, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, bad := range []string{"Password", "S4U", "HighestAvailable", "BootTrigger"} {
		if strings.Contains(document, bad) {
			t.Fatalf("unexpected %s", bad)
		}
	}
	if !strings.Contains(windowsTaskXML("sid", "app.exe", "", false), "<Enabled>false</Enabled>") {
		t.Fatal("disabled startup still registers an enabled logon trigger")
	}
}
