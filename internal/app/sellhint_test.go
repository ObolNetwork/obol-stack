package app

import (
	"strings"
	"testing"
)

func TestPrintSellHint_NeverForDvPod(t *testing.T) {
	env := newTestEnv(t, true)
	env.writeDeployment(t, "dvtee", "a", map[string]string{
		"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"),
		"values.yaml":   "a: 1\n",
	})
	calls := useFakeTools(t, func(tool, args string, _ []string, _ []byte) (string, error) {
		return "dvtee-a 3600,3610,3620,\n", nil
	})

	PrintSellHint(env.cfg, env.u, "dvtee/a")

	if strings.Contains(env.stdout.String(), "obol sell") {
		t.Errorf("sell hint printed for a dv-pod:\n%s", env.stdout)
	}

	if len(*calls) != 0 {
		t.Errorf("dv-pod chart should short-circuit before listing Services: %+v", *calls)
	}
}

func TestPrintSellHints_DenyListedPorts(t *testing.T) {
	env := newTestEnv(t, false)

	printSellHints(env.u, "myapp-x",
		"api 8080,\n"+
			"charon-like 3600,\n"+ // validator API
			"p2p 3610,\n"+
			"multi 8081,3600,\n"+ // any denied port excludes the Service
			"beacon 5052,\n"+
			"engine 8551,\n"+
			"signer 9000,\n"+
			"my-validator-ui 8082,\n"+ // validator in the name
			"web 80,\n")

	out := env.stdout.String()

	for _, want := range []string{"obol sell http api --upstream api --port 8080", "obol sell http web --upstream web --port 80"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing hint %q:\n%s", want, out)
		}
	}

	for _, bad := range []string{"--port 3600", "--port 3610", "multi", "--port 5052", "--port 8551", "--port 9000", "validator"} {
		if strings.Contains(out, bad) {
			t.Errorf("hint contains %q:\n%s", bad, out)
		}
	}
}

func TestUnsellableChart(t *testing.T) {
	for _, name := range []string{"dv-pod", "charon", "charon-cluster", "charon-relay", "remote-signer", "web3signer", "my-validator", "lido-signer-x"} {
		if !unsellableChart(name) {
			t.Errorf("%s should be unsellable", name)
		}
	}

	for _, name := range []string{"redis", "postgresql", "obol-app", ""} {
		if unsellableChart(name) {
			t.Errorf("%s should be sellable", name)
		}
	}
}
