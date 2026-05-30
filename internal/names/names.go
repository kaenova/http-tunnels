package names

import (
	"fmt"
	"math/rand"
	"strings"
)

// NameData contains the name lists
type NameData struct {
	LastNames   []string `json:"last_names"`
	FemaleNames []string `json:"female_names"`
	MaleNames   []string `json:"male_names"`
}

// Generator creates random subdomain names
type Generator struct {
	data    NameData
	rng     *rand.Rand
}

// NewGenerator creates a new name generator
func NewGenerator() *Generator {
	return &Generator{
		data:    defaultNames,
		rng:     rand.New(rand.NewSource(rand.Int63())),
	}
}

// Generate creates a random subdomain name
// Format: {lastname}-{firstname}-{4digit}
func (g *Generator) Generate() string {
	lastName := g.data.LastNames[g.rng.Intn(len(g.data.LastNames))]
	firstName := g.data.FemaleNames[g.rng.Intn(len(g.data.FemaleNames))]
	digits := g.rng.Intn(10000)

	lastName = sanitizeName(lastName)
	firstName = sanitizeName(firstName)

	return fmt.Sprintf("%s-%s-%04d", lastName, firstName, digits)
}

// GenerateWithSeed creates a deterministic subdomain name
func (g *Generator) GenerateWithSeed(seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	lastName := g.data.LastNames[rng.Intn(len(g.data.LastNames))]
	firstName := g.data.FemaleNames[rng.Intn(len(g.data.FemaleNames))]
	digits := rng.Intn(10000)

	lastName = sanitizeName(lastName)
	firstName = sanitizeName(firstName)

	return fmt.Sprintf("%s-%s-%04d", lastName, firstName, digits)
}

func sanitizeName(name string) string {
	// Remove spaces (for multi-word names like "Van Buren" -> "vanburen")
	name = strings.ReplaceAll(name, " ", "")
	name = strings.ReplaceAll(name, "-", "")
	name = strings.ReplaceAll(name, "'", "")
	name = strings.ReplaceAll(name, ".", "")
	return strings.ToLower(name)
}

// defaultNames is the embedded name data
var defaultNames = NameData{
	LastNames: []string{
		"smith", "johnson", "williams", "brown", "jones", "garcia", "miller", "davis",
		"rodriguez", "martinez", "hernandez", "lopez", "gonzalez", "wilson", "anderson",
		"thomas", "taylor", "moore", "jackson", "martin", "lee", "perez", "thompson",
		"white", "harris", "sanchez", "clark", "ramirez", "lewis", "robinson", "walker",
		"young", "allen", "king", "wright", "scott", "torres", "nguyen", "hill", "flores",
		"green", "adams", "nelson", "baker", "hall", "rivera", "campbell", "mitchell",
		"carter", "roberts", "gomez", "phillips", "evans", "turner", "diaz", "parker",
		"cruz", "edwards", "collins", "reyes", "stewart", "morris", "morales", "murphy",
		"cook", "rogers", "gutierrez", "ortiz", "morgan", "cooper", "peterson", "bailey",
		"reed", "kelly", "howard", "ramos", "kim", "cox", "ward", "richardson", "watson",
		"brooks", "chavez", "wood", "james", "bennett", "gray", "mendoza", "ruiz",
		"hughes", "price", "alvarez", "castillo", "sanders", "patel", "myers", "long",
		"ross", "foster", "jimenez",
	},
	FemaleNames: []string{
		"mary", "patricia", "jennifer", "linda", "barbara", "elizabeth", "susan", "jessica",
		"sarah", "karen", "lisa", "nancy", "betty", "margaret", "sandra", "ashley",
		"dorothy", "kimberly", "emily", "donna", "michelle", "carol", "amanda", "melissa",
		"deborah", "stephanie", "rebecca", "sharon", "laura", "cynthia", "kathleen",
		"amy", "angela", "shirley", "anna", "brenda", "pamela", "emma", "nicole",
		"helen", "samantha", "katherine", "christine", "debra", "rachel", "carolyn",
		"janet", "catherine", "maria", "heather", "diane", "ruth", "julie", "olivia",
		"joyce", "virginia", "victoria", "kelly", "lauren", "rose", "judith", "evelyn",
		"joan", "christina", "andrea", "francis", "alice", "jean", "martha", "ann",
		"jacqueline", "frances", "doris", "kathryn", "julia", "tiffany", "theresa",
		"elaine", "anne", "denise", "beverly", "mildred", "louise", "sara", "janice",
		"grace", "amber", "crystal", "marilyn", "jeanette", "renee", "lillian", "jane",
		"diana", "annie", "april", "alexis", "tina", "sherry",
	},
	MaleNames: []string{
		"james", "robert", "john", "michael", "david", "william", "richard", "joseph",
		"thomas", "christopher", "charles", "daniel", "matthew", "anthony", "mark",
		"donald", "steven", "paul", "andrew", "joshua", "kenneth", "kevin", "brian",
		"george", "timothy", "ronald", "edward", "jason", "jeffrey", "ryan", "jacob",
		"gary", "nicholas", "eric", "jonathan", "stephen", "larry", "justin", "scott",
		"brandon", "benjamin", "samuel", "raymond", "gregory", "frank", "alexander",
		"patrick", "jack", "dennis", "jerry", "tyler", "aaron", "jose", "nathan",
		"henry", "douglas", "peter", "adam", "zachary", "nathaniel", "kyle", "walter",
		"harold", "jeremy", "ethan", "carl", "keith", "roger", "gerald", "christian",
		"terry", "sean", "arthur", "austin", "noah", "lawrence", "jesse", "joe",
		"bryan", "billy", "jordan", "albert", "dylan", "bruce", "willie", "gabriel",
		"alan", "juan", "logan", "wayne", "ralph", "roy", "eugene", "randy", "vince",
		"russell", "mason", "philip", "louis", "bobby",
	},
}