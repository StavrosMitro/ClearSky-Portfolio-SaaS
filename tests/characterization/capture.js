// Runs the LEGACY parsing and histogram logic on every fixture and stores
// the result in golden/<fixture>.json. The two functions below are copied
// from the legacy services at commit 57cd8b5 and must not be "improved":
//   parseWorkbook  ← initial_grades/app.js, final_grades/app.js,
//                    stats_service/app.js, View_personal_grades/app.js
//   fetchHistogram ← stats_service/app.js (SQL ROUND/GROUP BY on MySQL)
// MySQL storage is modelled explicitly: grade is DECIMAL(4,2) and Q1..Q10 are
// INT, so values are rounded when stored (fixtures contain no .5 ties).
const XLSX = require('xlsx');
const fs = require('fs');
const path = require('path');

function parseWorkbook(buffer) {
  const wb   = XLSX.read(buffer, { type: 'buffer' });
  const rows = XLSX.utils.sheet_to_json(wb.Sheets[wb.SheetNames[0]], { header: 1, raw: false });
  if (rows.length < 4) throw new Error('Template too short');
  const weightRow = rows[1], headerRow = rows[2], dataRows = rows.slice(3);
  const map = {
    'Αριθμός Μητρώου':'AM', 'Ονοματεπώνυμο':'name',
    'Ακαδημαϊκό E-mail':'email','Περίοδος δήλωσης':'declarationPeriod',
    'Τμήμα Τάξης':'classTitle','Κλίμακα βαθμολόγησης':'gradingScale',
    'Βαθμολογία':'grade'
  };
  return dataRows.map(row => {
    const d = {};
    headerRow.forEach((t,i) => {
      const k = map[t?.trim()];
      if (!k) return;
      if (row[i] != null && row[i] !== '')
        d[k] = k === 'grade' ? parseFloat(row[i]) : row[i].toString().trim();
    });
    for (let q = 1; q <= 10; q++) {
      const idx    = 8 + (q - 1);
      const score  = parseFloat(row[idx]);
      const weight = parseFloat(weightRow[idx]);
      d[`Q${q}`] = (!isNaN(score) && !isNaN(weight)) ? score * weight : null;
    }
    return d;
  });
}

// MySQL: DECIMAL(4,2) keeps two decimals; INT rounds to the nearest integer.
const roundHalfAway = x => Math.sign(x) * Math.round(Math.abs(x));
const stored = doc => {
  const out = { ...doc, grade: doc.grade == null ? null : Math.round(doc.grade * 100) / 100 };
  for (let q = 1; q <= 10; q++) out[`Q${q}`] = doc[`Q${q}`] == null ? null : roundHalfAway(doc[`Q${q}`]);
  return out;
};

// fetchHistogram, with the SQL (GROUP BY ROUND(field)) evaluated in JS.
function fetchHistogram(field, storedRows) {
  const counts = new Map();
  for (const r of storedRows) {
    if (r[field] == null) continue; // SQL: NULL groups separately and never matches a bin
    const value = roundHalfAway(r[field]);
    counts.set(value, (counts.get(value) || 0) + 1);
  }
  const rows = [...counts.entries()].map(([value, count]) => ({ value, count }));
  const maxBin = field === 'grade' ? 10 : rows.reduce((max, r) => Math.max(max, +r.value), 0);
  const categories = Array.from({ length: maxBin + 1 }, (_, i) => i);
  const data = categories.map(i => {
    const found = rows.find(r => +r.value === i);
    return found ? +found.count : 0;
  });
  return { categories, data };
}

for (const name of fs.readdirSync(path.join(__dirname, 'fixtures')).sort()) {
  const docs = parseWorkbook(fs.readFileSync(path.join(__dirname, 'fixtures', name)));
  const storedRows = docs.map(stored);
  const histograms = {};
  for (const dim of ['grade', 'Q1', 'Q2', 'Q3', 'Q4', 'Q5', 'Q6', 'Q7', 'Q8', 'Q9', 'Q10']) {
    histograms[dim] = fetchHistogram(dim, storedRows);
  }
  const golden = { fixture: name, docs, histograms };
  fs.writeFileSync(path.join(__dirname, 'golden', name.replace(/\.xlsx$/, '.json')), JSON.stringify(golden, null, 2) + '\n');
  console.log(`golden/${name.replace(/\.xlsx$/, '.json')}: ${docs.length} rows`);
}
