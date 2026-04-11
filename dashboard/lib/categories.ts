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

  { match: "סול מרכזי", normalized: Category.Kids },
  { match: "גאחלנד", normalized: Category.Kids },
  { match: "גיילנד", normalized: Category.Kids },
  { match: "gayaland", normalized: Category.Kids },
  { match: "פינת חי", normalized: Category.Kids },
  { match: "אלמוג פנאי ומוזיקה", normalized: Category.Kids },
  { match: "יולי אנד איב", normalized: Category.Kids },

  { match: "upapp", normalized: Category.Leisure },
  { match: "audible", normalized: Category.Leisure },

  { match: "איבן בייקרי", normalized: Category.Restaurants },
  { match: "ibn bakery", normalized: Category.Restaurants },

  { match: "פחות מאלף", normalized: Category.Other },
  { match: "pchot", normalized: Category.Other },

  { match: "vectify", normalized: Category.Work },
  { match: "sqsp", normalized: Category.Work },
  { match: "squarespace", normalized: Category.Work },
  { match: "anthropic", normalized: Category.Work },

  { match: "פנגו", normalized: Category.Transport },
  { match: "pango", normalized: Category.Transport },

  { match: "אמישרגז", normalized: Category.Municipality },
];

// Provider-supplied category → normalized English category.
// Exact match after trim.
const rawCategoryMap: Record<string, string> = {
  // Groceries
  "מזון ומשקאות": Category.Groceries,
  "מזון וצריכה": Category.Groceries,
  "מזון מהיר": Category.Groceries,
  // Restaurants
  "מסעדות": Category.Restaurants,
  "מסעדות, קפה וברים": Category.Restaurants,
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
  "ציוד ומשרד": Category.Finance,
  "העברת כספים": Category.Finance,
  "משיכת מזומן": Category.Finance,
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
