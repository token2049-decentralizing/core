package main

import (
	"fmt"
	"path"
	"strings"
)

// Files that say nothing about contribution quality and burn tokens.
var skipFiles = []string{
	"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb", "go.sum",
	"Cargo.lock", "poetry.lock", "Gemfile.lock", "composer.lock",
}

var skipDirs = []string{"vendor/", "node_modules/", "dist/", "build/", ".next/"}

func skipped(file string) bool {
	if strings.HasSuffix(file, ".min.js") || strings.HasSuffix(file, ".min.css") || strings.HasSuffix(file, ".snap") {
		return true
	}
	for _, f := range skipFiles {
		if path.Base(file) == f {
			return true
		}
	}
	for _, d := range skipDirs {
		if strings.HasPrefix(file, d) || strings.Contains(file, "/"+d) {
			return true
		}
	}
	return false
}

// trimDiff drops noise files and caps the diff at maxChars, noting what was left out.
func trimDiff(diff string, maxChars int) string {
	var keep, dropped []string
	for _, section := range splitSections(diff) {
		if file := sectionFile(section); file != "" && skipped(file) {
			dropped = append(dropped, file)
			continue
		}
		keep = append(keep, section)
	}
	out := strings.Join(keep, "")
	if len(out) > maxChars {
		out = out[:maxChars] + fmt.Sprintf("\n[... diff truncated, %d more characters not shown]\n", len(out)-maxChars)
	}
	if len(dropped) > 0 {
		out += fmt.Sprintf("[omitted generated/lock files: %s]\n", strings.Join(dropped, ", "))
	}
	return out
}

func splitSections(diff string) []string {
	var sections []string
	start := 0
	for i := 0; i < len(diff); {
		next := strings.Index(diff[i+1:], "\ndiff --git ")
		if next < 0 {
			break
		}
		cut := i + 1 + next + 1
		sections = append(sections, diff[start:cut])
		start, i = cut, cut
	}
	return append(sections, diff[start:])
}

// sectionFile returns the "b/" path of a "diff --git a/x b/y" section.
func sectionFile(section string) string {
	line, _, _ := strings.Cut(section, "\n")
	if !strings.HasPrefix(line, "diff --git ") {
		return ""
	}
	if i := strings.LastIndex(line, " b/"); i >= 0 {
		return line[i+3:]
	}
	return ""
}
