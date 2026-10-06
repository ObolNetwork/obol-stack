package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"
)

// SuggestCommand prints an error for an unknown subcommand of parent and
// suggests similarly named sibling commands (Levenshtein distance <= 2).
func (u *UI) SuggestCommand(parent *cli.Command, command string) {
	u.Errorf("unknown command: %s", strings.TrimSpace(parent.FullName()+" "+command))

	suggestions := findSimilarCommands(parent.Commands, command, 2)
	if len(suggestions) > 0 {
		fmt.Fprintln(u.stderr)
		fmt.Fprintln(u.stderr, "Did you mean?")

		for _, s := range suggestions {
			fmt.Fprintf(u.stderr, "  %s %s\n", parent.FullName(), boldStyle.Render(s))
		}
	}

	fmt.Fprintln(u.stderr)
	fmt.Fprintln(u.stderr, dimStyle.Render(fmt.Sprintf("Run '%s --help' for a list of commands", parent.FullName())))
}

// findSimilarCommands returns the names of visible commands whose name or
// alias is within maxDist Levenshtein distance of the input (or that the
// input is a prefix of), best matches first: prefix matches, then by edit
// distance. At most three suggestions are returned.
func findSimilarCommands(commands []*cli.Command, input string, maxDist int) []string {
	type match struct {
		name  string
		score int
	}

	var matches []match

	for _, cmd := range commands {
		if cmd.Hidden {
			continue
		}

		best := -1
		for _, name := range cmd.Names() {
			if name == input {
				best = -1
				break
			}
			score := -1
			if len(input) >= 2 && strings.HasPrefix(name, input) {
				score = 0
			} else if dist := levenshtein(input, name); dist <= maxDist {
				score = dist
			}
			if score >= 0 && (best < 0 || score < best) {
				best = score
			}
		}
		if best >= 0 {
			matches = append(matches, match{cmd.Name, best})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score < matches[j].score })

	var results []string
	for i, m := range matches {
		if i == 3 {
			break
		}
		results = append(results, m.name)
	}

	return results
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}

	if lb == 0 {
		return la
	}

	// Use single-row DP.
	prev := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr := make([]int, lb+1)
		curr[0] = i

		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}

		prev = curr
	}

	return prev[lb]
}
