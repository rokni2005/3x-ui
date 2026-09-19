package bot

// Fruit is one item of the order catalog, priced per kilogram (Toman).
type Fruit struct {
	ID    string
	Emoji string
	Name  string
	Price int
}

// Catalog is the full list of fruits customers can order.
var Catalog = []Fruit{
	{"watermelon", "🍉", "هندوانه", 45000},
	{"cantaloupe", "🍈", "طالبی", 55000},
	{"shapasand", "🍈", "شاپسند", 58000},
	{"peach", "🍑", "هلو", 325000},
	{"nectarine", "🍑", "شلیل", 2800000},
	{"cherry", "🍒", "گیلاس", 550000},
	{"sourcherry", "🍒", "آلبالو", 480000},
	{"grape", "🍇", "انگور", 275000},
	{"strawberry", "🍓", "توت‌فرنگی", 380000},
	{"apricot", "🍑", "زردآلو", 340000},
	{"mango", "🥭", "انبه", 430000},
	{"pineapple", "🍍", "آناناس", 265000},
	{"kiwi", "🥝", "کیوی", 290000},
	{"pear", "🍐", "گلابی", 350000},
	{"apple_golab", "🍎", "سیب گلاب", 340000},
	{"apple", "🍎", "سیب", 310000},
	{"melon", "🍈", "خربزه", 65000},
	{"fig", "🟣", "انجیر تازه", 285000},
	{"plum", "🟠", "آلو (قرمز، زرد، سبز)", 295000},
}

// FindFruit looks up a fruit by its stable ID, or nil if not found.
func FindFruit(id string) *Fruit {
	for i := range Catalog {
		if Catalog[i].ID == id {
			return &Catalog[i]
		}
	}
	return nil
}
