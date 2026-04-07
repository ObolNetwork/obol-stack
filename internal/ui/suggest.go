package ui

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

// SuggestCommand prints an error for an unknown command and suggests
// similar commands based on Levenshtein distance.
func (u *UI) SuggestCommand(app *cli.App, command string) {
	u.Errorf("unknown command: %s", command)

	suggestions := findSimilarCommands(app.Commands, command, 2)
	if len(suggestions) > 0 {
		fmt.Fprintln(u.stderr)
		fmt.Fprintln(u.stderr, "Did you mean?")

		for _, s := range suggestions {
			fmt.Fprintf(u.stderr, "  obol %s\n", boldStyle.Render(s))
		}
	}

	fmt.Fprintln(u.stderr)
	u.Dim("Run 'obol --help' for a list of commands")
}

// findSimilarCommands returns command names within maxDist Levenshtein
// distance of the input, searching recursively through subcommands.
func findSimilarCommands(commands []*cli.Command, input string, maxDist int) []string {
	var results []string

	for _, cmd := range commands {
		if cmd.Hidden {
			continue
		}

		dist := levenshtein(input, cmd.Name)
		if dist > 0 && dist <= maxDist {
			results = append(results, cmd.Name)
		}
		// Also check aliases.
		for _, alias := range cmd.Aliases {
			dist := levenshtein(input, alias)
			if dist > 0 && dist <= maxDist {
				results = append(results, cmd.Name)
				break
			}
		}
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
