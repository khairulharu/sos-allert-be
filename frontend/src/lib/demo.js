// Seeded demo reports for the jury demo. These only fill the
// textarea; the triage itself always comes from the backend.
export const DEMO_EXAMPLES = [
  {
    label: 'Turis Inggris — kecelakaan motor (Canggu)',
    message: 'Help! My friend fell off his scooter near Batu Bolong, leg bleeding, can\'t move',
    area: 'Canggu, Bali'
  },
  {
    label: 'Warga lokal — orang tenggelam',
    message: 'Tolong, ada orang tenggelam di pantai, terseret ombak',
    area: 'Kuta, Bali'
  },
  {
    label: 'Laporan ambigu — AI harus menaikkan urgensi',
    message: 'sakit banget, tolong',
    area: 'Denpasar, Bali'
  }
];

export const CATEGORIES = [
  'ACCIDENT_TRAFFIC',
  'MEDICAL',
  'DROWNING',
  'FIRE',
  'DANGER',
  'NATURAL_DISASTER',
  'OTHER'
];

// Plain-language labels shown to reporters and responders. The
// category code itself stays in the payload and on the result card.
export const CATEGORY_LABELS = {
  ACCIDENT_TRAFFIC: 'Kecelakaan lalu lintas',
  MEDICAL: 'Medis',
  DROWNING: 'Tenggelam',
  FIRE: 'Kebakaran',
  DANGER: 'Bahaya',
  NATURAL_DISASTER: 'Bencana alam',
  OTHER: 'Lainnya'
};
