// Builds the fixture workbooks in the legacy e-sec template:
//   row 0: title, row 1: question weights (from column 8), row 2: headers,
//   rows 3+: one student per row.
// Weights come from {0.2, 0.4, 0.6, 0.8, 1.0, 1.2} and raw scores are
// integers, so no weighted score ends in .5 and totals never end in .50:
// MySQL's rounding of ties is platform-dependent, so ties are avoided.
const XLSX = require('xlsx');
const path = require('path');

let seed = 20260927;
const random = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648);
const pick = list => list[Math.floor(random() * list.length)];

const HEADERS = ['Αριθμός Μητρώου', 'Ονοματεπώνυμο', 'Ακαδημαϊκό E-mail', 'Περίοδος δήλωσης',
  'Τμήμα Τάξης', 'Κλίμακα βαθμολόγησης', 'Βαθμολογία', ''];
const FIRST = ['Γιώργος', 'Μαρία', 'Νίκος', 'Ελένη', 'Κώστας', 'Άννα', 'Δημήτρης', 'Σοφία', 'Γιάννης', 'Κατερίνα'];
const LAST = ['Παπαδόπουλος', 'Ιωάννου', 'Γεωργίου', 'Νικολάου', 'Δημητρίου', 'Κωνσταντίνου', 'Αλεξίου'];

function total() {
  // a number with two decimals, never x.50
  let value;
  do { value = Math.round(random() * 1000) / 100; } while (Math.round(value * 100) % 50 === 0 && value % 1 !== 0);
  return value;
}

// Each student's grades are drawn once, so an initial and a final workbook of
// the same course differ only by the explicit overrides.
function students(count, prefix, questions, blankRate = 0) {
  return Array.from({ length: count }, (_, i) => {
    const am = `${prefix}${String(21001 + i).padStart(5, '0')}`;
    const name = `${pick(FIRST)} ${pick(LAST)}`;
    const scores = Array.from({ length: questions }, () => (random() < blankRate ? '' : Math.floor(random() * 11)));
    return { am, name, email: `el${am.slice(-5)}@mail.uni.example`, total: total(), scores };
  });
}

function workbook({ course, period, weights, people, overrides = {} }) {
  const rows = [
    ['ΒΑΘΜΟΛΟΓΙΟ', course, period],
    [...Array(8).fill(''), ...weights],
    [...HEADERS, ...weights.map((_, i) => `Q${i + 1}`)],
  ];
  for (const person of people) {
    rows.push([person.am, person.name, person.email, period, course, '0-10',
      overrides[person.am] ?? person.total, '', ...person.scores]);
  }
  const wb = XLSX.utils.book_new();
  XLSX.utils.book_append_sheet(wb, XLSX.utils.aoa_to_sheet(rows), 'Βαθμολόγιο');
  return wb;
}

const physicsPeople = students(30, '0311', 4);
const softwarePeople = students(25, '0312', 10, 0.1);
const fixtures = {
  'physics-initial.xlsx': workbook({ course: 'ΦΥΣΙΚΗ   (3101)', period: '2024-2025 ΧΕΙΜ 2024', weights: [0.4, 0.6, 1.0, 0.8], people: physicsPeople }),
  // After review, two students get a different total in the final upload.
  'physics-final.xlsx': workbook({ course: 'ΦΥΣΙΚΗ   (3101)', period: '2024-2025 ΧΕΙΜ 2024', weights: [0.4, 0.6, 1.0, 0.8], people: physicsPeople,
    overrides: { [physicsPeople[0].am]: 9.2, [physicsPeople[1].am]: 4.8 } }),
  'software-initial.xlsx': workbook({ course: 'ΤΕΧΝΟΛΟΓΙΑ ΛΟΓΙΣΜΙΚΟΥ   (3205)', period: '2024-2025 ΕΑΡ 2025',
    weights: [0.2, 0.4, 0.6, 0.8, 1.0, 1.2, 0.2, 0.4, 0.6, 0.8], people: softwarePeople }),
};
for (const [name, wb] of Object.entries(fixtures)) {
  XLSX.writeFile(wb, path.join(__dirname, 'fixtures', name));
  console.log('wrote fixtures/' + name);
}
