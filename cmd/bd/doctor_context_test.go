package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/utils"
)

func savePersistentPreRunState(t *testing.T) {
	t.Helper()

	// Track env vars that PersistentPreRun may modify via prepareSelectedCommandContext,
	// applyChangeDirSelection, or preserveRedirectSourceDatabase. Restoring them
	// prevents routing state from leaking into subsequent tests.
	for _, key := range []string{"BEADS_DIR", "BEADS_DB", "BD_DB", "BEADS_DOLT_SERVER_DATABASE"} {
		t.Setenv(key, os.Getenv(key))
	}

	oldServerMode := serverMode
	oldCmdCtx := cmdCtx
	oldDBPath := dbPath
	oldActor := actor
	oldJSONOutput := jsonOutput
	oldReadonlyMode := readonlyMode
	oldDoltAutoCommit := doltAutoCommit
	flagState := snapshotRootFlagState()
	t.Cleanup(func() {
		serverMode = oldServerMode
		cmdCtx = oldCmdCtx
		dbPath = oldDBPath
		actor = oldActor
		jsonOutput = oldJSONOutput
		readonlyMode = oldReadonlyMode
		doltAutoCommit = oldDoltAutoCommit
		restoreRootFlagState(t, flagState)
	})

	serverMode = false
	cmdCtx = nil
	dbPath = ""
	actor = ""
	jsonOutput = false
	readonlyMode = false
	doltAutoCommit = ""
}

func writeMetadataConfig(t *testing.T, beadsDir string, doltMode string, database string) {
	t.Helper()

	if err := (&configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     doltMode,
		DoltDatabase: database,
	}).Save(beadsDir); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
}

func TestSavePersistentPreRunStateRestoresRedirectDatabase(t *testing.T) {
	const databaseEnv = "BEADS_DOLT_SERVER_DATABASE"
	const sourceDatabase = "source_db"
	const inheritedDatabase = "inherited_db"

	for _, tc := range []struct {
		name       string
		value      string
		present    bool
		wantDuring string
	}{
		{name: "absent", present: false, wantDuring: sourceDatabase},
		{name: "empty", present: true, wantDuring: sourceDatabase},
		{name: "inherited", value: inheritedDatabase, present: true, wantDuring: inheritedDatabase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(databaseEnv, tc.value)
			if !tc.present {
				if err := os.Unsetenv(databaseEnv); err != nil {
					t.Fatalf("unset database env: %v", err)
				}
			}

			t.Run("redirect", func(t *testing.T) {
				savePersistentPreRunState(t)
				repoDir := t.TempDir()
				sourceBeadsDir := filepath.Join(repoDir, "source", ".beads")
				targetBeadsDir := filepath.Join(repoDir, "target", ".beads")
				writeTestConfigYAML(t, sourceBeadsDir, "")
				writeMetadataConfig(t, sourceBeadsDir, configfile.DoltModeServer, sourceDatabase)
				writeTestConfigYAML(t, targetBeadsDir, "")
				writeMetadataConfig(t, targetBeadsDir, configfile.DoltModeServer, "target_db")
				if err := os.WriteFile(filepath.Join(sourceBeadsDir, beads.RedirectFileName), []byte(targetBeadsDir+"\n"), 0o600); err != nil {
					t.Fatalf("write redirect: %v", err)
				}

				preserveRedirectSourceDatabase(sourceBeadsDir)
				if got, present := os.LookupEnv(databaseEnv); !present || got != tc.wantDuring {
					t.Fatalf("redirect database = (%q, %t), want (%q, true)", got, present, tc.wantDuring)
				}
			})

			if got, present := os.LookupEnv(databaseEnv); present != tc.present || got != tc.value {
				t.Errorf("database env after child cleanup = (%q, %t), want (%q, %t)", got, present, tc.value, tc.present)
			}
		})
	}
}

func TestDoctorPersistentPreRunLoadsServerModeForNoDBCommand(t *testing.T) {
	repoDir := t.TempDir()
	beadsDir := filepath.Join(repoDir, ".beads")
	writeTestConfigYAML(t, beadsDir, "")
	writeMetadataConfig(t, beadsDir, configfile.DoltModeServer, "doctor_ctx_test")

	t.Chdir(repoDir)
	t.Setenv("BEADS_DIR", beadsDir)
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)
	savePersistentPreRunState(t)

	if rootCmd.PersistentPreRunE == nil {
		t.Fatal("rootCmd.PersistentPreRunE must be set")
	}
	if err := rootCmd.PersistentPreRunE(doctorCmd, nil); err != nil {
		t.Fatalf("PersistentPreRunE: %v", err)
	}

	if !serverMode {
		t.Fatal("doctor should load server mode before the no-store early return")
	}
}

func TestDoctorPersistentPreRunUsesExplicitDBTarget(t *testing.T) {
	callerRepo := filepath.Join(t.TempDir(), "caller")
	callerBeadsDir := filepath.Join(callerRepo, ".beads")
	writeTestConfigYAML(t, callerBeadsDir, "")
	writeMetadataConfig(t, callerBeadsDir, configfile.DoltModeEmbedded, "caller_ctx_test")

	targetRepo := filepath.Join(t.TempDir(), "target")
	targetBeadsDir := filepath.Join(targetRepo, ".beads")
	writeTestConfigYAML(t, targetBeadsDir, "")
	writeMetadataConfig(t, targetBeadsDir, configfile.DoltModeServer, "target_ctx_test")

	t.Chdir(callerRepo)
	t.Setenv("BEADS_DIR", callerBeadsDir)
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)
	savePersistentPreRunState(t)

	targetDBPath := filepath.Join(targetBeadsDir, "dolt")
	dbPath = targetDBPath
	if flag := rootCmd.PersistentFlags().Lookup("db"); flag != nil {
		flag.Changed = true
	}

	if err := rootCmd.PersistentPreRunE(doctorCmd, nil); err != nil {
		t.Fatalf("PersistentPreRunE: %v", err)
	}

	if got := os.Getenv("BEADS_DIR"); got != targetBeadsDir {
		t.Fatalf("BEADS_DIR = %q, want %q", got, targetBeadsDir)
	}
	if !serverMode {
		t.Fatal("doctor should use the explicit target repo's server mode")
	}
}

func TestBootstrapPersistentPreRunUsesExplicitDBTarget(t *testing.T) {
	callerRepo := filepath.Join(t.TempDir(), "caller")
	callerBeadsDir := filepath.Join(callerRepo, ".beads")
	writeTestConfigYAML(t, callerBeadsDir, "")
	writeMetadataConfig(t, callerBeadsDir, configfile.DoltModeEmbedded, "caller_bootstrap_test")

	targetRepo := filepath.Join(t.TempDir(), "target")
	targetBeadsDir := filepath.Join(targetRepo, ".beads")
	writeTestConfigYAML(t, targetBeadsDir, "")
	writeMetadataConfig(t, targetBeadsDir, configfile.DoltModeServer, "target_bootstrap_test")

	t.Chdir(callerRepo)
	t.Setenv("BEADS_DIR", callerBeadsDir)
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)
	savePersistentPreRunState(t)

	targetDBPath := filepath.Join(targetBeadsDir, "dolt")
	dbPath = targetDBPath
	if flag := rootCmd.PersistentFlags().Lookup("db"); flag != nil {
		flag.Changed = true
	}

	if err := rootCmd.PersistentPreRunE(bootstrapCmd, nil); err != nil {
		t.Fatalf("PersistentPreRunE: %v", err)
	}

	if got := os.Getenv("BEADS_DIR"); got != targetBeadsDir {
		t.Fatalf("BEADS_DIR = %q, want %q", got, targetBeadsDir)
	}
}

func TestLoadSelectionEnvironmentUsesAmbientEnvFileForBEADSDB(t *testing.T) {
	callerRepo := filepath.Join(t.TempDir(), "caller")
	callerBeadsDir := filepath.Join(callerRepo, ".beads")
	writeTestConfigYAML(t, callerBeadsDir, "")

	targetRepo := filepath.Join(t.TempDir(), "target")
	targetBeadsDir := filepath.Join(targetRepo, ".beads")
	writeTestConfigYAML(t, targetBeadsDir, "")
	targetDBPath := filepath.Join(targetBeadsDir, "dolt")
	if err := os.MkdirAll(targetDBPath, 0o700); err != nil {
		t.Fatalf("mkdir target db dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(callerBeadsDir, ".env"), []byte("BEADS_DB="+targetDBPath+"\n"), 0o600); err != nil {
		t.Fatalf("write caller .env: %v", err)
	}

	t.Chdir(callerRepo)
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", "")
	t.Setenv("BD_DB", "")

	loadSelectionEnvironment()

	if got := os.Getenv("BEADS_DB"); utils.CanonicalizePath(got) != utils.CanonicalizePath(targetDBPath) {
		t.Fatalf("BEADS_DB = %q, want %q", got, targetDBPath)
	}
	if got := beads.FindDatabasePath(); utils.CanonicalizePath(got) != utils.CanonicalizePath(targetDBPath) {
		t.Fatalf("FindDatabasePath() = %q, want %q", got, targetDBPath)
	}
}
func TestSelectedDoltBeadsDirUsesReboundBEADSDir(t *testing.T) {
	callerRepo := filepath.Join(t.TempDir(), "caller")
	callerBeadsDir := filepath.Join(callerRepo, ".beads")
	writeTestConfigYAML(t, callerBeadsDir, "")

	targetRepo := filepath.Join(t.TempDir(), "target")
	targetBeadsDir := filepath.Join(targetRepo, ".beads")
	writeTestConfigYAML(t, targetBeadsDir, "")
	targetDBPath := filepath.Join(targetBeadsDir, "dolt")
	if err := os.MkdirAll(targetDBPath, 0o700); err != nil {
		t.Fatalf("mkdir target db dir: %v", err)
	}

	t.Chdir(callerRepo)
	t.Setenv("BEADS_DIR", targetBeadsDir)
	t.Setenv("BEADS_DB", filepath.Join(callerBeadsDir, "dolt"))

	if got := selectedDoltBeadsDir(); utils.CanonicalizePath(got) != utils.CanonicalizePath(targetBeadsDir) {
		t.Fatalf("selectedDoltBeadsDir() = %q, want %q", got, targetBeadsDir)
	}
}
