// Package categories provides shared category normalization rules used by
// both the agent (Go) and the dashboard (TypeScript). The TS version in
// dashboard/lib/categories.ts MUST be kept in sync with this file.
package categories

import "strings"

// Normalized category names (English) used for aggregation + display.
const (
	Groceries     = "Groceries"
	Restaurants   = "Restaurants"
	Transport     = "Transport & Fuel"
	Insurance     = "Insurance"
	Healthcare    = "Healthcare"
	Home          = "Home & Furniture"
	Electronics   = "Electronics & Telecom"
	Fashion       = "Fashion"
	Leisure       = "Leisure"
	Travel        = "Travel"
	Municipality  = "Municipality & Gov"
	Pets          = "Pets"
	Kids          = "Kids"
	BooksMedia    = "Books & Media"
	Beauty        = "Beauty"
	Finance       = "Finance"
	Work          = "Work"
	Other         = "Other"
)

// merchantOverrides: specific merchants whose normalized category differs
// from what Cal/Max report. Match is case-insensitive substring on the
// merchant description. First match wins.
var merchantOverrides = []struct {
	match      string // lowercase substring
	normalized string
}{
	{"wolt", Groceries},
	{"האסישוק", Groceries},
	{"hacishuq", Groceries},
	{"משק קירשנר", Groceries},
	{"kirshner", Groceries},
	{"גליקסמן", Groceries},

	{"קיי אס פי", Home},
	{"ksp", Home},
	{"המשרדיה", Home},
	{"סטוק סנטר", Home},

	{"בוימן", Pets},
	{"boyman", Pets},
	{"זו סנטר", Pets},
	{"מיץ פטל פטס", Pets},
	{"יובל סמואל", Pets},

	{"סול מרכזי", Kids},
	{"גאחלנד", Kids},
	{"גיילנד", Kids},
	{"gayaland", Kids},
	{"פינת חי", Kids},
	{"אלמוג פנאי ומוזיקה", Kids},
	{"יולי אנד איב", Kids},
	{"הפיראט האדום", Kids},
	{"טויס", Kids},
	{"toys", Kids},

	{"upapp", Leisure},
	{"audible", Leisure},

	{"איבן בייקרי", Restaurants},
	{"ibn bakery", Restaurants},

	{"פחות מאלף", Other},
	{"pchot", Other},
	{"רד בוקס", Other},
	{"red box", Other},

	{"vectify", Work},
	{"sqsp", Work},
	{"squarespace", Work},
	{"anthropic", Work},

	{"פנגו", Transport},
	{"pango", Transport},
	{"חניון", Transport},

	{"אמישרגז", Municipality},
	{"חברת החשמל", Municipality},

	{"ארנק מט", Finance},
	{"מכירת מט", Finance},
}

// rawCategoryMap maps provider-supplied category strings (Cal + Max, Hebrew)
// to normalized English names. Keys are compared as exact-match after trim.
var rawCategoryMap = map[string]string{
	// Groceries
	"מזון ומשקאות":   Groceries,
	"מזון וצריכה":    Groceries,
	"מזון מהיר":      Groceries,

	// Restaurants
	"מסעדות":             Restaurants,
	"מסעדות, קפה וברים":  Restaurants,

	// Transport & Fuel
	"רכב ותחבורה":   Transport,
	"תחבורה ורכבים": Transport,
	"אנרגיה":        Transport,
	"דלק, חשמל וגז":  Transport,

	// Insurance
	"ביטוח":          Insurance,
	"ביטוח ופיננסים": Insurance,

	// Healthcare
	"רפואה ובריאות":     Healthcare,
	"רפואה ובתי מרקחת":  Healthcare,

	// Home & Furniture
	"ריהוט ובית":  Home,
	"עיצוב הבית":  Home,

	// Electronics & Telecom
	"תקשורת ומחשבים": Electronics,
	"חשמל ומחשבים":   Electronics,
	"שירותי תקשורת":   Electronics,

	// Fashion
	"אופנה": Fashion,

	// Leisure
	"פנאי בילוי":          Leisure,
	"פנאי, בידור וספורט":  Leisure,

	// Travel
	"תיירות":          Travel,
	"מלונאות ואירוח":  Travel,
	"טיסות ותיירות":   Travel,

	// Municipality & Gov
	"מוסדות":        Municipality,
	"עירייה וממשלה": Municipality,

	// Pets
	"חיות מחמד": Pets,

	// Kids
	"ילדים": Kids,

	// Books & Media
	"ספרים ודפוס": BooksMedia,

	// Beauty
	"טיפוח ויופי":     Beauty,
	"קוסמטיקה וטיפוח": Beauty,

	// Finance
	"פיננסים":     Finance,
	"העברת כספים": Finance,
	"משיכת מזומן": Finance,

	// Office/equipment — falls through to Other (rare in this dataset).
	"ציוד ומשרד": Other,
}

// Normalize returns the canonical English category for a given merchant +
// raw category. Merchant overrides take precedence over category mapping.
// Unknown inputs fall through to "Other".
func Normalize(merchant, rawCategory string) string {
	merchantLower := strings.ToLower(strings.TrimSpace(merchant))
	for _, rule := range merchantOverrides {
		if strings.Contains(merchantLower, rule.match) {
			return rule.normalized
		}
	}
	if norm, ok := rawCategoryMap[strings.TrimSpace(rawCategory)]; ok {
		return norm
	}
	return Other
}
