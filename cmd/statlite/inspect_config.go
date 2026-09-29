package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/pvrlabs/statlite/internal/inspect"
	"gopkg.in/yaml.v3"
)

// Go's flag parser stops at the URL. Move known options before positionals,
// keeping each option's value attached, so both documented orders work.
func reorderInspectArgs(args []string) ([]string, error) {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if arg == "--type" || arg == "-type" || arg == "--name" || arg == "-name" || arg == "--create-config" || arg == "-create-config" || arg == "--add-to-config" || arg == "-add-to-config" {
				if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
					return nil, fmt.Errorf("%s requires a value", arg)
				}
				i++
				flags = append(flags, args[i])
			}
		} else {
			positionals = append(positionals, arg)
		}
	}
	return append(flags, positionals...), nil
}

func suggestedInspectionTarget(result *inspect.Result, name string) (suggestedTarget, inspectionTargetPresentation, error) {
	if result == nil {
		return suggestedTarget{}, inspectionTargetPresentation{}, errors.New("inspection returned no result")
	}
	presentation, err := inspectionPresentation(result.TargetType)
	if err != nil {
		return suggestedTarget{}, inspectionTargetPresentation{}, err
	}
	if name == "" {
		name, err = derivedTargetName(presentation.targetType, result.Endpoint)
		if err != nil {
			return suggestedTarget{}, inspectionTargetPresentation{}, err
		}
	}
	name = strings.TrimSpace(name)
	target := suggestedTarget{Name: name, Type: presentation.targetType, URL: result.Endpoint}
	if _, err := renderSuggestedTargetConfig(target, presentation.errorContext); err != nil {
		return suggestedTarget{}, inspectionTargetPresentation{}, err
	}
	return target, presentation, nil
}

func derivedTargetName(targetType, endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("cannot derive target name from endpoint %q", endpoint)
	}
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	host := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return '-'
	}, u.Hostname())
	host = strings.Trim(host, "-")
	if host == "" {
		return "", fmt.Errorf("cannot derive target name from endpoint %q", endpoint)
	}
	return targetType + "-" + host + "-" + port, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func monitorCommand(path string) string {
	if path == "statlite.yaml" {
		return "statlite"
	}
	return "statlite --config " + shellQuote(path)
}

func inspectWriteCommand(rawURL, endpoint, typed string, target suggestedTarget, mode string) string {
	command := "statlite inspect " + shellQuote(rawURL)
	if typed != "" {
		command += " --type " + shellQuote(typed)
	}
	// The default name is stable, so the command can omit --name.
	if derived, err := derivedTargetName(target.Type, endpoint); err == nil && derived != target.Name {
		command += " --name " + shellQuote(target.Name)
	}
	return command + " " + mode + " ./statlite.yaml"
}

func createInspectionConfig(path string, target suggestedTarget, errorContext string) error {
	content, err := renderSuggestedTargetConfig(target, errorContext)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("cannot create config %q: %w", path, err)
	}
	_, writeErr := io.WriteString(file, content)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("writing config %q: %w", path, errors.Join(writeErr, closeErr))
	}
	return nil
}

func addInspectionTarget(path string, target suggestedTarget) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot stat config %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config %q is not a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read config %q: %w", path, err)
	}
	block, err := inspectionTargetAppend(content, target)
	if err != nil {
		return fmt.Errorf("config %q: %w", path, err)
	}
	// Validate before opening the existing file for an in-place append.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("cannot open config %q for append: %w", path, err)
	}
	written, writeErr := file.Write(block)
	if writeErr == nil && written != len(block) {
		writeErr = io.ErrShortWrite
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("cannot append to config %q: %w", path, err)
	}
	return nil
}

func inspectionTargetAppend(content []byte, target suggestedTarget) ([]byte, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("cannot parse YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("only a single YAML document is supported for automatic append")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("automatic append requires a top-level mapping")
	}
	root := document.Content[0]
	if len(root.Content) < 2 || root.Content[len(root.Content)-2].Value != "targets" {
		return nil, errors.New("automatic append requires targets: as the last top-level section; copy the target manually")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "targets" && i != len(root.Content)-2 {
			return nil, errors.New("duplicate targets: section; copy the target manually")
		}
	}
	key := root.Content[len(root.Content)-2]
	list := root.Content[len(root.Content)-1]
	if list.Kind != yaml.SequenceNode || len(list.Content) == 0 || list.Style != 0 || list.Anchor != "" {
		return nil, errors.New("automatic append requires targets: to be a normal nonempty list; copy the target manually")
	}
	lines := bytes.Split(content, []byte("\n"))
	for _, rawLine := range lines {
		line := bytes.TrimSuffix(rawLine, []byte("\r"))
		if bytes.Equal(line, []byte("...")) || bytes.HasPrefix(line, []byte("... ")) || bytes.HasPrefix(line, []byte("...\t")) {
			return nil, errors.New("automatic append cannot follow a YAML ... document terminator; add the target manually")
		}
	}
	if key.Line < 1 || key.Line > len(lines) || !bytes.HasPrefix(lines[key.Line-1], []byte("targets:")) {
		return nil, errors.New("automatic append requires an unindented targets: section; copy the target manually")
	}
	indent := ""
	for _, item := range list.Content {
		if item.Kind != yaml.MappingNode || item.Style != 0 || item.Line < 1 || item.Line > len(lines) {
			return nil, errors.New("automatic append requires normal target mappings; copy the target manually")
		}
		line := lines[item.Line-1]
		spaces := len(line) - len(bytes.TrimLeft(line, " "))
		if spaces == 0 || !bytes.HasPrefix(line[spaces:], []byte("- ")) || bytes.Contains(line[:spaces], []byte("\t")) {
			return nil, errors.New("automatic append requires a normal targets list; copy the target manually")
		}
		if indent == "" {
			indent = string(line[:spaces])
		} else if indent != string(line[:spaces]) {
			return nil, errors.New("target list indentation is inconsistent; copy the target manually")
		}
		var existing struct {
			Name            string `yaml:"name"`
			URL             string `yaml:"url"`
			ActuatorBaseURL string `yaml:"actuator_base_url"`
		}
		if err := item.Decode(&existing); err != nil {
			return nil, fmt.Errorf("cannot read existing target: %w", err)
		}
		if hasConfigEnvReference(existing.Name) || hasConfigEnvReference(existing.URL) || hasConfigEnvReference(existing.ActuatorBaseURL) {
			return nil, errors.New("cannot safely check duplicate target names or endpoints when an existing target uses environment variables in name, url, or actuator_base_url; add the target manually")
		}
		if strings.TrimSpace(existing.Name) == strings.TrimSpace(target.Name) {
			return nil, fmt.Errorf("target name %q already exists", target.Name)
		}
		for _, endpoint := range []string{existing.URL, existing.ActuatorBaseURL} {
			if endpoint != "" && sameEndpoint(endpoint, target.URL) {
				return nil, fmt.Errorf("endpoint %q is already configured under target %q", target.URL, existing.Name)
			}
		}
	}
	newline := "\n"
	if bytes.Contains(content, []byte("\r\n")) {
		newline = "\r\n"
	}
	entry, err := yaml.Marshal([]suggestedTarget{target})
	if err != nil {
		return nil, fmt.Errorf("cannot render target: %w", err)
	}
	var block strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(string(entry), "\n"), "\n") {
		block.WriteString(indent)
		block.WriteString(line)
		block.WriteString(newline)
	}
	var result []byte
	if len(content) > 0 && content[len(content)-1] != '\n' {
		result = append(result, []byte(newline)...)
	}
	return append(result, []byte(block.String())...), nil
}

var configEnvReference = regexp.MustCompile(`\$\{[^}]*\}|\$[A-Za-z0-9_]+|\$\$`)

func hasConfigEnvReference(value string) bool {
	// Config loading treats $${ as a literal ${ rather than a variable.
	return configEnvReference.MatchString(strings.ReplaceAll(value, "$${", ""))
}

func sameEndpoint(left, right string) bool {
	parse := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return raw
		}
		u.Scheme = strings.ToLower(u.Scheme)
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else if u.Scheme == "http" {
				port = "80"
			}
		}
		u.Host = net.JoinHostPort(strings.ToLower(u.Hostname()), port)
		return u.String()
	}
	return parse(left) == parse(right)
}
