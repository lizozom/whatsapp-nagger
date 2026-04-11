// Category normalization rules. This file MUST be kept in sync with
// internal/categories/categories.go in the Go module. If you change one,
// change the other.

export const Category = {
  Groceries: "Groceries",
  Restaurants: "Restaurants",
  Transport: "Transport & Fuel",
  Insurance: "Insurance",
  Healthcare: "Healthcare",
  Home: "Home & Furniture",
  Electronics: "Electronics & Telecom",
  Fashion: "Fashion",
  Leisure: "Leisure",
  Travel: "Travel",
  Municipality: "Municipality & Gov",
  Pets: "Pets",
  Kids: "Kids",
  BooksMedia: "Books & Media",
  Beauty: "Beauty",
  Finance: "Finance",
  Work: "Work",
  Other: "Other",
} as const;

// Fixed color per category — stable across sorts, cycles, and chart types.
// Chosen for semantic hinting: green = fresh/money, red = medical, etc.
export const categoryColors: Record<string, string> = {
  [Category.Groceries]: "hsl(145, 60%, 45%)", // green
  [Category.Restaurants]: "hsl(25, 85%, 55%)", // orange
  [Category.Transport]: "hsl(210, 70%, 50%)", // blue
  [Category.Insurance]: "hsl(270, 50%, 55%)", // purple
  [Category.Healthcare]: "hsl(0, 70%, 55%)", // red
  [Category.Home]: "hsl(30, 45%, 45%)", // brown
  [Category.Electronics]: "hsl(190, 70%, 50%)", // cyan
  [Category.Fashion]: "hsl(325, 70%, 60%)", // pink
  [Category.Leisure]: "hsl(50, 85%, 55%)", // yellow
  [Category.Travel]: "hsl(175, 65%, 45%)", // teal
  [Category.Municipality]: "hsl(220, 10%, 50%)", // gray
  [Category.Pets]: "hsl(35, 75%, 55%)", // amber
  [Category.Kids]: "hsl(290, 65%, 60%)", // violet
  [Category.BooksMedia]: "hsl(235, 55%, 55%)", // indigo
  [Category.Beauty]: "hsl(340, 70%, 65%)", // magenta
  [Category.Finance]: "hsl(155, 50%, 35%)", // dark green
  [Category.Work]: "hsl(215, 25%, 40%)", // slate
  [Category.Other]: "hsl(220, 8%, 65%)", // light gray
};

export function getCategoryColor(category: string): string {
  return categoryColors[category] || categoryColors[Category.Other];
}

// Merchant-level overrides (checked first). Case-insensitive substring match
// on merchant description.
const merchantOverrides: Array<{ match: string; normalized: string }> = [
  { match: "wolt", normalized: Category.Groceries },
  { match: "האסישוק", normalized: Category.Groceries },
  { match: "hacishuq", normalized: Category.Groceries },
  { match: "משק קירשנר", normalized: Category.Groceries },
  { match: "kirshner", normalized: Category.Groceries },
  { match: "גליקסמן", normalized: Category.Groceries },

  { match: "קיי אס פי", normalized: Category.Home },
  { match: "ksp", normalized: Category.Home },
  { match: "המשרדיה", normalized: Category.Home },
  { match: "סטוק סנטר", normalized: Category.Home },

  { match: "בוימן", normalized: Category.Pets },
  { match: "boyman", normalized: Category.Pets },
  { match: "זו סנטר", normalized: Category.Pets },
  { match: "מיץ פטל פטס", normalized: Category.Pets },
  { match: "יובל סמואל", normalized: Category.Pets },

  { match: "סול מרכזי", normalized: Category.Kids },
  { match: "גאחלנד", normalized: Category.Kids },
  { match: "גיילנד", normalized: Category.Kids },
  { match: "gayaland", normalized: Category.Kids },
  { match: "פינת חי", normalized: Category.Kids },
  { match: "אלמוג פנאי ומוזיקה", normalized: Category.Kids },
  { match: "יולי אנד איב", normalized: Category.Kids },
  { match: "הפיראט האדום", normalized: Category.Kids },
  { match: "טויס", normalized: Category.Kids },
  { match: "toys", normalized: Category.Kids },

  { match: "upapp", normalized: Category.Leisure },
  { match: "audible", normalized: Category.Leisure },

  { match: "איבן בייקרי", normalized: Category.Restaurants },
  { match: "ibn bakery", normalized: Category.Restaurants },
  { match: "מקדונלדס", normalized: Category.Restaurants },
  { match: "mcdonalds", normalized: Category.Restaurants },

  { match: "פחות מאלף", normalized: Category.Other },
  { match: "pchot", normalized: Category.Other },
  { match: "רד בוקס", normalized: Category.Other },
  { match: "red box", normalized: Category.Other },

  { match: "vectify", normalized: Category.Work },
  { match: "sqsp", normalized: Category.Work },
  { match: "squarespace", normalized: Category.Work },
  { match: "anthropic", normalized: Category.Work },

  { match: "פנגו", normalized: Category.Transport },
  { match: "pango", normalized: Category.Transport },
  { match: "חניון", normalized: Category.Transport },

  { match: "אמישרגז", normalized: Category.Municipality },
  { match: "חברת החשמל", normalized: Category.Municipality },

  { match: "ארנק מט", normalized: Category.Finance },
  { match: "מכירת מט", normalized: Category.Finance },
];

// Provider-supplied category → normalized English category.
// Exact match after trim.
const rawCategoryMap: Record<string, string> = {
  // Groceries
  "מזון ומשקאות": Category.Groceries,
  "מזון וצריכה": Category.Groceries,
  // Restaurants
  "מסעדות": Category.Restaurants,
  "מסעדות, קפה וברים": Category.Restaurants,
  "מזון מהיר": Category.Restaurants, // fast food = restaurants
  // Transport & Fuel
  "רכב ותחבורה": Category.Transport,
  "תחבורה ורכבים": Category.Transport,
  "אנרגיה": Category.Transport,
  "דלק, חשמל וגז": Category.Transport,
  // Insurance
  "ביטוח": Category.Insurance,
  "ביטוח ופיננסים": Category.Insurance,
  // Healthcare
  "רפואה ובריאות": Category.Healthcare,
  "רפואה ובתי מרקחת": Category.Healthcare,
  // Home & Furniture
  "ריהוט ובית": Category.Home,
  "עיצוב הבית": Category.Home,
  // Electronics & Telecom
  "תקשורת ומחשבים": Category.Electronics,
  "חשמל ומחשבים": Category.Electronics,
  "שירותי תקשורת": Category.Electronics,
  // Fashion
  "אופנה": Category.Fashion,
  // Leisure
  "פנאי בילוי": Category.Leisure,
  "פנאי, בידור וספורט": Category.Leisure,
  // Travel
  "תיירות": Category.Travel,
  "מלונאות ואירוח": Category.Travel,
  "טיסות ותיירות": Category.Travel,
  // Municipality & Gov
  "מוסדות": Category.Municipality,
  "עירייה וממשלה": Category.Municipality,
  // Pets
  "חיות מחמד": Category.Pets,
  // Kids
  "ילדים": Category.Kids,
  // Books & Media
  "ספרים ודפוס": Category.BooksMedia,
  // Beauty
  "טיפוח ויופי": Category.Beauty,
  "קוסמטיקה וטיפוח": Category.Beauty,
  // Finance
  "פיננסים": Category.Finance,
  "העברת כספים": Category.Finance,
  "משיכת מזומן": Category.Finance,

  // Office/equipment — rare, falls through to Other.
  "ציוד ומשרד": Category.Other,
};

/**
 * Returns the canonical English category for a given merchant + raw category.
 * Merchant overrides take precedence. Unknown inputs fall through to "Other".
 */
export function normalizeCategory(
  merchant: string,
  rawCategory: string,
): string {
  const m = merchant.toLowerCase().trim();
  for (const rule of merchantOverrides) {
    if (m.includes(rule.match)) {
      return rule.normalized;
    }
  }
  return rawCategoryMap[rawCategory.trim()] ?? Category.Other;
}
