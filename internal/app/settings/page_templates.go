package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SubscriptionPageTemplates lists the bundled subscription page templates
// (templates/subscription/*.html) by the name the subscription_page_template
// setting accepts, for example "subscription/midnight.html".
func SubscriptionPageTemplates() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(appTemplateBasePath(), "subscription", "*.html"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if info, err := os.Stat(match); err != nil || info.IsDir() {
			continue
		}
		names = append(names, "subscription/"+filepath.Base(match))
	}
	sort.Strings(names)
	return names, nil
}

// ReadSubscriptionPageTemplate returns the content of a bundled subscription
// page template. Only names under subscription/ are accepted, and the name is
// resolved inside the templates directory so it cannot reach other files.
func ReadSubscriptionPageTemplate(name string) (string, error) {
	normalized, err := normalizeTemplateName(name)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(normalized, "subscription/") || !strings.HasSuffix(normalized, ".html") {
		return "", fmt.Errorf("%w: %s", ErrTemplateNotFound, name)
	}
	path, err := resolveAppTemplatePath(normalized)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(content), "\r\n", "\n"), nil
}
