package ai

// Guide is the short first-response guidance shown with a triage
// result. Steps are general first-aid information only, not medical
// advice for a specific person, and every guide carries the
// disclaimer that official emergency services must still be called.
// Guides are static content keyed by category: they reuse the single
// triage result instead of spending a second AI call.
type Guide struct {
	Category     string
	TitleID      string
	TitleEN      string
	StepsID      []string
	StepsEN      []string
	DisclaimerID string
	DisclaimerEN string
}

const disclaimerID = "Panduan umum saja, bukan pengganti tenaga medis. Hubungi layanan darurat resmi dan jangan pindahkan korban dengan cedera serius kecuali ada bahaya langsung."
const disclaimerEN = "General guidance only, not a substitute for medical professionals. Call official emergency services and do not move a seriously injured person unless there is immediate danger."

// GuideFor returns the guide for a category, falling back to the
// generic guide for unknown categories.
func GuideFor(category string) Guide {
	if g, ok := guides[category]; ok {
		return g
	}
	return guides[CategoryOther]
}

var guides = map[string]Guide{
	CategoryAccidentTraffic: {
		Category: CategoryAccidentTraffic,
		TitleID:  "Kecelakaan lalu lintas: yang bisa dilakukan sekarang",
		TitleEN:  "Traffic accident: what to do now",
		StepsID: []string{
			"Amankan lokasi: nyalakan lampu hazard, jauhkan orang dari jalur kendaraan, dan jangan berdiri di tengah jalan.",
			"Periksa kesadaran dan pernapasan korban, tekan pendarahan dengan kain bersih tanpa mengangkatnya.",
			"Jangan pindahkan korban yang diduga patah tulang atau cedera leher, kecuali kendaraan terbakar atau ada bahaya langsung.",
			"Catat waktu, lokasi, dan kondisi korban untuk disampaikan ke petugas.",
		},
		StepsEN: []string{
			"Secure the scene: turn on hazard lights, keep people out of traffic, and do not stand in the road.",
			"Check whether the person is conscious and breathing, and press on bleeding with a clean cloth.",
			"Do not move someone with a suspected fracture or neck injury unless the vehicle is on fire or there is immediate danger.",
			"Note the time, location, and the person's condition for emergency responders.",
		},
		DisclaimerID: disclaimerID, DisclaimerEN: disclaimerEN,
	},
	CategoryMedical: {
		Category: CategoryMedical,
		TitleID:  "Darurat medis: yang bisa dilakukan sekarang",
		TitleEN:  "Medical emergency: what to do now",
		StepsID: []string{
			"Tetap tenang, pastikan korban bernapas, dan longgarkan pakaian yang mengikat.",
			"Jika korban tidak sadar tapi bernapas, miringkan tubuhnya dan pantau terus sampai bantuan datang.",
			"Jangan memberi makan atau minum pada korban yang tidak sadar.",
			"Sampaikan riwayat singkat yang kamu tahu: sejak kapan, gejala apa, dan obat atau alergi jika diketahui.",
		},
		StepsEN: []string{
			"Stay calm, check that the person is breathing, and loosen tight clothing.",
			"If the person is unconscious but breathing, turn them on their side and keep watching them until help arrives.",
			"Do not give food or drink to an unconscious person.",
			"Share what you know briefly: when it started, the symptoms, and any known medication or allergy.",
		},
		DisclaimerID: disclaimerID, DisclaimerEN: disclaimerEN,
	},
	CategoryDrowning: {
		Category: CategoryDrowning,
		TitleID:  "Bahaya di air: yang bisa dilakukan sekarang",
		TitleEN:  "Water danger: what to do now",
		StepsID: []string{
			"Jangan berenang mengejar korban jika arusnya kuat. Lempar pelampung, tali, atau benda mengapung dari tepi.",
			"Teriak minta bantuan penjaga pantai dan orang di sekitar, tunjuk satu orang untuk menelepon layanan darurat.",
			"Setelah korban keluar dari air, periksa napasnya dan mulai pertolongan sesuai pelatihan yang kamu miliki.",
			"Jaga korban tetap hangat dan jangan tinggalkan sendirian.",
		},
		StepsEN: []string{
			"Do not swim after the person in a strong current. Throw a float, rope, or floating object from shore.",
			"Shout for a lifeguard and bystanders, and ask one specific person to call emergency services.",
			"Once the person is out of the water, check breathing and help within the limits of your training.",
			"Keep the person warm and do not leave them alone.",
		},
		DisclaimerID: disclaimerID, DisclaimerEN: disclaimerEN,
	},
	CategoryFire: {
		Category: CategoryFire,
		TitleID:  "Kebakaran: yang bisa dilakukan sekarang",
		TitleEN:  "Fire: what to do now",
		StepsID: []string{
			"Utamakan keluar dari bangunan, jangan kembali mengambil barang.",
			"Bergerak merunduk di bawah asap, tutup hidung dan mulut dengan kain basah jika memungkinkan.",
			"Jangan membuka pintu yang terasa panas, dan jangan menggunakan lift.",
			"Setelah aman, laporkan lokasi dan apakah ada orang yang masih di dalam.",
		},
		StepsEN: []string{
			"Get out first. Do not go back inside for belongings.",
			"Stay low under smoke, and cover your nose and mouth with a damp cloth if you can.",
			"Do not open a hot door, and do not use lifts.",
			"Once safe, report the location and whether anyone is still inside.",
		},
		DisclaimerID: disclaimerID, DisclaimerEN: disclaimerEN,
	},
	CategoryDanger: {
		Category: CategoryDanger,
		TitleID:  "Situasi berbahaya: yang bisa dilakukan sekarang",
		TitleEN:  "Dangerous situation: what to do now",
		StepsID: []string{
			"Utamakan keselamatan diri: menjauh ke tempat ramai dan terang, jangan melawan pelaku bersenjata.",
			"Jangan mengejar pelaku. Perhatikan ciri-ciri, arah kabur, dan kendaraan yang digunakan dari jarak aman.",
			"Segera hubungi layanan darurat dan sampaikan lokasi serta apa yang terjadi.",
			"Tenangkan korban dan jangan menyentuh barang bukti.",
		},
		StepsEN: []string{
			"Put your own safety first: move to a crowded, well-lit place and do not confront an armed person.",
			"Do not chase anyone. From a safe distance, note distinguishing features, the direction they went, and any vehicle.",
			"Call emergency services right away with the location and what happened.",
			"Calm the victim and do not touch evidence.",
		},
		DisclaimerID: disclaimerID, DisclaimerEN: disclaimerEN,
	},
	CategoryNaturalDisaster: {
		Category: CategoryNaturalDisaster,
		TitleID:  "Bencana alam: yang bisa dilakukan sekarang",
		TitleEN:  "Natural disaster: what to do now",
		StepsID: []string{
			"Saat gempa, lindungi kepala dan menjauh dari kaca serta benda yang bisa jatuh. Setelah guncangan berhenti, keluar ke area terbuka.",
			"Saat banjir, menjauh dari aliran air dan kabel listrik, jangan mengemudi melewati genangan dalam.",
			"Ikuti arahan evakuasi resmi dan bawa dokumen penting serta obat-obatan jika sempat dan aman.",
			"Periksa kondisi orang di sekitarmu dan laporkan yang terluka atau terjebak.",
		},
		StepsEN: []string{
			"During an earthquake, protect your head and stay away from glass and falling objects. Once shaking stops, move to open ground.",
			"During flooding, stay away from moving water and power lines, and do not drive through deep water.",
			"Follow official evacuation directions and take important documents and medication if it is safe and quick.",
			"Check on the people around you and report anyone injured or trapped.",
		},
		DisclaimerID: disclaimerID, DisclaimerEN: disclaimerEN,
	},
	CategoryOther: {
		Category: CategoryOther,
		TitleID:  "Darurat: yang bisa dilakukan sekarang",
		TitleEN:  "Emergency: what to do now",
		StepsID: []string{
			"Tetap tenang dan jelaskan apa yang terjadi, di mana, dan siapa yang butuh bantuan.",
			"Pastikan korban bernapas dan dalam posisi aman, lalu pantau kondisinya.",
			"Hubungi layanan darurat resmi dan sampaikan lokasi sejelas mungkin.",
			"Jangan meninggalkan korban sendirian sampai bantuan datang.",
		},
		StepsEN: []string{
			"Stay calm and describe what happened, where, and who needs help.",
			"Make sure the person is breathing and in a safe position, then keep watching them.",
			"Call official emergency services and describe the location as clearly as you can.",
			"Do not leave the person alone until help arrives.",
		},
		DisclaimerID: disclaimerID, DisclaimerEN: disclaimerEN,
	},
}
