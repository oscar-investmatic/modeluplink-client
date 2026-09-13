package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/hostexec"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
	"golang.org/x/sys/windows"
)

const schedulerScript = `[Console]::InputEncoding=[Text.UTF8Encoding]::new($false)
$ErrorActionPreference='Stop'
$p=[Console]::In.ReadToEnd() | ConvertFrom-Json
$s=New-Object -ComObject Schedule.Service
$s.Connect()
$f=$s.GetFolder('\')
switch ($p.action) {
 'register' { $t=$f.RegisterTask($p.name,$p.xml,6,$p.sid,$null,3,$null); if($p.run){$null=$t.Run($null)} }
 'stop' { $matches=@($f.GetTasks(0) | Where-Object {$_.Name -ceq $p.name});if($matches.Count -eq 0){exit 0};$t=$matches[0];$t.Enabled=$false;$t.Stop(0);for($i=0;$i -lt 50;$i++){if($t.GetInstances(0).Count -eq 0){$f.DeleteTask($p.name,0);exit 0};Start-Sleep -Milliseconds 100};throw 'Task is still running' }
 'retire' {$matches=@($f.GetTasks(0) | Where-Object {$_.Name -ceq $p.name});if($matches.Count -eq 0){exit 0};$t=$matches[0];$t.Enabled=$false;$f.DeleteTask($p.name,0)}
 'startup' {foreach($t in $f.GetTasks(0)){if($t.Name.StartsWith($p.prefix)){ $d=$t.Definition;foreach($trigger in $d.Triggers){$trigger.Enabled=$p.enabled};$null=$f.RegisterTaskDefinition($t.Name,$d,6,$p.sid,$null,3,$null)}}}
 default {throw 'Unknown scheduler operation'}
}`

var schedulerCall = func(input map[string]any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := hostexec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", schedulerScript)
	cmd.Stdin = bytes.NewReader(data)
	if _, err = cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Windows could not update background sharing: %w", err)
	}
	return nil
}

func ownerSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}
func taskName(slug string) (string, string, error) {
	if slug != "@ollama" && slug != "@ui" {
		if err := security.ValidateSlug(slug); err != nil {
			return "", "", err
		}
	}
	sid, err := ownerSID()
	kind := "agent-" + slug
	if slug == "@ollama" {
		kind = "runtime-ollama"
	}
	if slug == "@ui" {
		kind = "interface"
	}
	return "ModelUplink-" + sid + "-" + kind, sid, err
}
func registerTask(slug, executable string, args []string, run bool) error {
	name, sid, err := taskName(slug)
	if err != nil {
		return err
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windows.EscapeArg(arg)
	}
	return schedulerCall(map[string]any{"action": "register", "name": name, "sid": sid, "xml": windowsTaskXML(sid, executable, strings.Join(quoted, " "), !cfg.StartupDisabled), "run": run})
}
func taskAction(slug, action string) error {
	name, _, err := taskName(slug)
	if err != nil {
		return err
	}
	return schedulerCall(map[string]any{"action": action, "name": name})
}
func Install(executable, configPath, slug string) (string, error) {
	if err := security.ValidateSlug(slug); err != nil {
		return "", err
	}
	if err := localconfig.PrivateDirectory(filepath.Join(filepath.Dir(configPath), slug+"-certificates")); err != nil {
		return "", err
	}
	if err := registerTask(slug, executable, []string{"_agent", "--config", configPath}, true); err != nil {
		return "", err
	}
	app := filepath.Join(filepath.Dir(executable), "modeluplink-app.exe")
	if _, e := os.Stat(app); e == nil {
		if err := registerTask("@ui", app, []string{"--background"}, false); err != nil {
			return "", errors.Join(err, taskAction(slug, "stop"))
		}
	}
	return LogPath(slug)
}
func Stop(slug string) error          { return taskAction(slug, "stop") }
func StopSharing(slug string) error   { return Stop(slug) }
func Uninstall(slug string) error     { return Stop(slug) }
func RetireCurrent(slug string) error { return taskAction(slug, "retire") }
func SetStartup(enabled bool) error {
	sid, err := ownerSID()
	if err != nil {
		return err
	}
	return schedulerCall(map[string]any{"action": "startup", "sid": sid, "prefix": "ModelUplink-" + sid + "-", "enabled": enabled})
}
func LogPath(slug string) (string, error) {
	if slug != "@ollama" && slug != "@ui" {
		if err := security.ValidateSlug(slug); err != nil {
			return "", err
		}
	}
	path, err := localconfig.Path()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(path), "logs")
	if err = localconfig.PrivateDirectory(dir); err != nil {
		return "", err
	}
	name := "agent-" + slug
	if slug == "@ollama" {
		name = "runtime-ollama"
	}
	return filepath.Join(dir, name+".log"), nil
}
func RedirectAgentLog(slug string) (func(), error) {
	path, err := LogPath(slug)
	if err != nil {
		return nil, err
	}
	if stat, e := os.Stat(path); e == nil && stat.Size() > 10<<20 {
		_ = os.Remove(path + ".old")
		if e = os.Rename(path, path+".old"); e != nil {
			return nil, e
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = file, file
	return func() { os.Stdout, os.Stderr = stdout, stderr; file.Close() }, nil
}

type managedRuntime struct{ Executable, Models string }

func runtimePath() (string, error) {
	p, err := localconfig.Path()
	return filepath.Join(filepath.Dir(p), "ollama-runtime.json"), err
}
func InstallOllama(executable string) (string, error) {
	path, err := runtimePath()
	if err != nil {
		return "", err
	}
	if err = localconfig.PrivateDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	models := os.Getenv("OLLAMA_MODELS")
	if models == "" {
		models = filepath.Join(home, ".ollama", "models")
	}
	models, err = filepath.Abs(models)
	if err != nil {
		return "", err
	}
	spec, _ := json.Marshal(managedRuntime{Executable: executable, Models: models})
	if err = os.WriteFile(path, spec, 0600); err != nil {
		return "", err
	}
	helper, err := os.Executable()
	if err != nil {
		return "", err
	}
	if err = registerTask("@ollama", helper, []string{"_managed-ollama"}, true); err != nil {
		return "", err
	}
	return LogPath("@ollama")
}
func RefreshManagedOllama(string) (bool, error) { return false, nil }
func RunManagedOllama(ctx context.Context) error {
	path, err := runtimePath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var spec managedRuntime
	if err = json.Unmarshal(data, &spec); err != nil {
		return err
	}
	if !filepath.IsAbs(spec.Executable) || !filepath.IsAbs(spec.Models) {
		return errors.New("invalid managed runtime path")
	}
	done, err := RedirectAgentLog("@ollama")
	if err != nil {
		return err
	}
	defer done()
	cmd := hostexec.CommandContext(ctx, spec.Executable, "serve")
	cmd.Env = append(os.Environ(), "OLLAMA_HOST=127.0.0.1:11434", "OLLAMA_MODELS="+spec.Models, "OLLAMA_MAX_LOADED_MODELS=1", "OLLAMA_NUM_PARALLEL=1", "OLLAMA_MAX_QUEUE=4", "OLLAMA_DEBUG=false")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return hostexec.RunInJob(cmd)
}

func StopManagedOllama() error { return taskAction("@ollama", "stop") }
func StopInterface() error     { return taskAction("@ui", "stop") }
