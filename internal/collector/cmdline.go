package collector

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// maxCmdline bounds a stored command line. The process list is written every
// process tick and kept with the raw samples, and a prompt passed as an
// argument would otherwise cost kilobytes per process every few seconds.
const maxCmdline = 1024

// masked stands in for a value that looked like a secret.
const masked = "***"

// secretWords mark an argument whose value is not shown. A name is matched by
// substring, so --api-key, HF_TOKEN and --client-secret are all caught; the
// price is that --max-num-batched-tokens is masked too, and a hidden number
// costs less than a shown key. The Datadog Agent's process scrubber keeps a
// similar list, narrower: its api_key does not match vLLM's --api-key, and it
// has no bare token.
var secretWords = []string{"key", "token", "secret", "password", "passwd"}

// readCmdline returns the command line of a process with secret values
// masked, or nothing when it cannot be read: the process has exited, or the
// container does not share the host PID namespace.
func readCmdline(pid uint32) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	return formatCmdline(data)
}

// formatCmdline turns the NUL separated arguments of /proc/<pid>/cmdline into
// one line to show. Masking happens here, where the line is read, so an agent
// never sends a secret to its hub. Each argument is also split into words,
// because one argument can hold a whole command: the code after python -c, or
// a shell line after sh -c.
func formatCmdline(raw []byte) string {
	s := strings.TrimRight(string(raw), "\x00")
	if s == "" {
		return ""
	}
	args := strings.Split(s, "\x00")
	out := make([]string, 0, len(args))
	maskNext := false
	for _, arg := range args {
		if maskNext && !strings.HasPrefix(arg, "-") {
			// The value of a secret flag is masked whole, spaces and all.
			out = append(out, masked)
			maskNext = false
			continue
		}
		words := strings.Fields(arg)
		for i, w := range words {
			if i > 0 && isSecretFlag(words[i-1]) && !strings.HasPrefix(w, "-") {
				words[i] = masked
				continue
			}
			words[i] = maskWord(w)
		}
		maskNext = len(words) > 0 && isSecretFlag(words[len(words)-1])
		out = append(out, strings.Join(words, " "))
	}
	return cut(strings.Join(out, " "), maxCmdline)
}

// isSecretFlag reports a flag whose value follows it as the next word.
func isSecretFlag(w string) bool {
	return strings.HasPrefix(w, "-") && !strings.Contains(w, "=") && isSecretName(w)
}

func isSecretName(name string) bool {
	name = strings.ToLower(name)
	for _, s := range secretWords {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

// maskWord masks the value of name=value when the name sounds secret, and
// the credentials of a URL wherever one appears.
func maskWord(w string) string {
	if i := strings.IndexByte(w, '='); i > 0 && isSecretName(w[:i]) {
		return w[:i+1] + masked
	}
	return maskURLCredentials(w)
}

func maskURLCredentials(w string) string {
	i := strings.Index(w, "://")
	if i < 0 {
		return w
	}
	host := i + len("://")
	end := len(w)
	if j := strings.IndexAny(w[host:], "/?#"); j >= 0 {
		end = host + j
	}
	at := strings.LastIndexByte(w[host:end], '@')
	if at < 0 {
		return w
	}
	return w[:host] + masked + w[host+at:]
}

// cut shortens s to at most n bytes without splitting a character, and says
// that it did.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
