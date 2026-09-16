package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chobits02/provena/internal/config"
)

const exampleConfigName = "config.example.yaml"

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path of the config file to create")
	force := fs.Bool("force", false, "Overwrite an existing config file")
	_ = fs.Parse(args)

	target := strings.TrimSpace(*configPath)
	if target == "" {
		target = defaultConfigPath
	}

	if _, err := os.Stat(target); err == nil && !*force {
		return fmt.Errorf("%s already exists; pass -force to overwrite", target)
	}

	src := locateExampleConfig(filepath.Dir(target))
	if src == "" {
		return fmt.Errorf("cannot find %s; run init from the repository root", exampleConfigName)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", src, err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", target, err)
	}

	fmt.Printf("Created %s from %s\n\n", target, src)
	fmt.Println("Next steps:")
	fmt.Println("  1. Set your model credentials, either in the file or via environment:")
	fmt.Println("       export PROVENA_API_KEY=...")
	fmt.Println("       # then point ai.channels.<id>.api_key at ${PROVENA_API_KEY}")
	fmt.Println("  2. provena doctor          # verify environment and configuration")
	fmt.Println("  3. provena serve           # start the console")
	return nil
}

// locateExampleConfig looks for the bundled example next to the target file,
// then in the working directory, then one level up.
func locateExampleConfig(dir string) string {
	candidates := []string{
		filepath.Join(dir, exampleConfigName),
		exampleConfigName,
		filepath.Join("..", exampleConfigName),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func runConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: provena config validate [-config path]")
	}
	switch args[0] {
	case "validate":
		return runConfigValidate(args[1:])
	default:
		return fmt.Errorf("unknown config subcommand %q (expected: validate)", args[0])
	}
}

func runConfigValidate(args []string) error {
	fs := flag.NewFlagSet("config validate", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path to the configuration file")
	_ = fs.Parse(args)

	cp := strings.TrimSpace(*configPath)
	if cp == "" {
		cp = defaultConfigPath
	}

	cfg, err := config.Load(cp)
	if err != nil {
		return fmt.Errorf("%s is not valid: %w", cp, err)
	}

	problems := make([]string, 0)

	// ResolveAIChannel expands ${VAR} references, so an unset variable surfaces
	// here as an empty credential rather than as a literal placeholder.
	if oa, channelID, found := cfg.ResolveAIChannel(""); !found {
		problems = append(problems, "no usable AI channel: set ai.default_channel and ai.channels")
	} else if strings.TrimSpace(oa.APIKey) == "" {
		problems = append(problems, fmt.Sprintf(
			"channel %q has no api_key (set it directly or via an environment variable)", channelID))
	} else if strings.TrimSpace(oa.Model) == "" {
		problems = append(problems, fmt.Sprintf("channel %q has no model", channelID))
	}

	if cfg.Server.Port < 0 || cfg.Server.Port > 65535 {
		problems = append(problems, fmt.Sprintf("server.port %d is out of range", cfg.Server.Port))
	}

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  - %s\n", p)
		}
		return fmt.Errorf("%s loaded but %d problem(s) were found", cp, len(problems))
	}

	fmt.Printf("%s is valid\n", cp)
	return nil
}
