package boss

import (
	"hash/fnv"
	"strings"
)

var engineerNamePool = []string{
	"Ada",
	"Grace",
	"Hedy",
	"Katherine",
	"Margaret",
	"Radia",
	"Frances",
	"Barbara",
	"Evelyn",
	"Lynn",
	"Sophie",
	"Emmy",
	"Alan",
	"Ken",
	"Dennis",
	"Tim",
	"Niklaus",
	"Tem",
	"Umon",
	"Kenzo",
	"Yumi",
	"Shiba",
	"Jun",
}

func EngineerNameForKey(parts ...string) string {
	hash := fnv.New32a()
	wrote := false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if wrote {
			_, _ = hash.Write([]byte{0})
		}
		_, _ = hash.Write([]byte(part))
		wrote = true
	}
	if !wrote {
		return "Engineer"
	}
	return engineerNamePool[int(hash.Sum32())%len(engineerNamePool)]
}
