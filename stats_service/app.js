require('dotenv').config();
const amqp = require('amqplib');
const mysql = require('mysql2/promise');
const XLSX = require('xlsx');

const rpcOK = data => ({ version: 1, data });
const rpcError = (code, message, retryable = false) => ({
  version: 1,
  error: { code, message, retryable }
});

/* const {
  MYSQL_URI,
  RABBITMQ_URI,
  RABBITMQ_EXCHANGE,
  RABBITMQ_ROUTING_KEY,
  RABBITMQ_SEND_AVAIL_KEY,
  RABBITMQ_GET_GRADES_KEY
} = process.env;

if (!MYSQL_URI || !RABBITMQ_URI || !RABBITMQ_EXCHANGE || !RABBITMQ_ROUTING_KEY) {
  console.error('❌  Missing required environment variables');
  process.exit(1);
}
 */

const {
  MYSQL_URI,
  RABBITMQ_URI,
  RABBITMQ_EXCHANGE,
  RABBITMQ_ROUTING_KEY,
  RABBITMQ_SEND_AVAIL_KEY,
  RABBITMQ_GET_GRADES_KEY
} = process.env;

const missingVars = [];

if (!MYSQL_URI) missingVars.push('MYSQL_URI');
if (!RABBITMQ_URI) missingVars.push('RABBITMQ_URI');
if (!RABBITMQ_EXCHANGE) missingVars.push('RABBITMQ_EXCHANGE');
if (!RABBITMQ_ROUTING_KEY) missingVars.push('RABBITMQ_ROUTING_KEY');

if (missingVars.length > 0) {
  console.error(`❌ Missing required environment variables: ${missingVars.join(', ')}`);
  process.exit(1);
}


(async () => {
  let connection;
  try {
    connection = await mysql.createConnection(MYSQL_URI);
    console.log('✅  Connected to MySQL');
  } catch (err) {
    console.error('❌ Failed to connect to MySQL:', err.message);
    process.exit(1);
  }

  let conn, channel;
  try {
    conn = await amqp.connect(RABBITMQ_URI);
    channel = await conn.createChannel();
    await channel.assertExchange(RABBITMQ_EXCHANGE, 'direct', { durable: true });
    await channel.assertExchange('clearsky.dlx.v1', 'direct', { durable: true });
    console.log('✅  Connected to RabbitMQ and exchange set');
  } catch (err) {
    console.error('❌ RabbitMQ connection/setup failed:', err.message);
    process.exit(1);
  }

  const makeReply = msg => payload => {
    const { replyTo, correlationId } = msg.properties;
    if (!replyTo) return;
    try {
      channel.publish('', replyTo, Buffer.from(JSON.stringify(payload)), {
        contentType: 'application/json',
        correlationId
      });
      console.log(`📤  Reply sent to ${replyTo} (corrId: ${correlationId})`);
    } catch (err) {
      console.error('❌ Failed to send reply:', err.message);
    }
  };

  const handleSubmissionLog = async msg => {
    const reply = makeReply(msg);
    try {
      const [rows] = await connection.execute('SELECT * FROM submission_log');
      reply(rpcOK(rows));
      channel.ack(msg);
    } catch (err) {
      reply(rpcError('DEPENDENCY_UNAVAILABLE', 'Statistics store is unavailable', true));
      channel.nack(msg, false, false);
    }
  };
  const handleGradeQuery = async msg => {
    const reply = makeReply(msg);
    try {
      const params = JSON.parse(msg.content.toString());
      const { declarationPeriod, classTitle } = params;
      if (!declarationPeriod || !classTitle) throw new Error('INVALID_QUERY');
      const dims = ['grade', 'Q1', 'Q2', 'Q3', 'Q4', 'Q5', 'Q6', 'Q7', 'Q8', 'Q9', 'Q10'];
      const result = {};
      for (const dim of dims) result[dim] = await fetchHistogram(dim, connection, params);
      reply(rpcOK(result));
      channel.ack(msg);
    } catch (err) {
      const invalid = err.message === 'INVALID_QUERY' || err instanceof SyntaxError;
      reply(rpcError(invalid ? 'INVALID_REQUEST' : 'DEPENDENCY_UNAVAILABLE', invalid ? 'Declaration period and class title are required' : 'Statistics store is unavailable', !invalid));
      channel.nack(msg, false, false);
    }
  };

   const q = 'clearsky.stats.commands.v1';
  await channel.assertQueue(q, { durable: true, arguments: { 'x-dead-letter-exchange': 'clearsky.dlx.v1', 'x-dead-letter-routing-key': 'clearsky.stats.commands.v1.dead' } });
  await channel.assertQueue('clearsky.stats.commands.v1.dlq', { durable: true });
  await channel.bindQueue('clearsky.stats.commands.v1.dlq', 'clearsky.dlx.v1', 'clearsky.stats.commands.v1.dead');
  await channel.bindQueue(q, RABBITMQ_EXCHANGE, RABBITMQ_ROUTING_KEY);
  await channel.bindQueue(q, RABBITMQ_EXCHANGE, RABBITMQ_SEND_AVAIL_KEY);
  await channel.bindQueue(q, RABBITMQ_EXCHANGE, RABBITMQ_GET_GRADES_KEY);
  channel.prefetch(10);
  console.log(`🚀  Listening for grades on "${RABBITMQ_ROUTING_KEY}"`);

  channel.consume(q, async msg => {
    if (!msg) return;
    if (msg.fields.routingKey === RABBITMQ_SEND_AVAIL_KEY) return handleSubmissionLog(msg);
    if (msg.fields.routingKey === RABBITMQ_GET_GRADES_KEY) return handleGradeQuery(msg);
    console.log('📩  Received grade message');
    const reply = makeReply(msg);

    const ct = (msg.properties.contentType || '').toLowerCase().trim();
    const buffer = (ct.includes('spreadsheet') || ct === 'application/octet-stream')
      ? msg.content
      : Buffer.from(msg.content.toString(), 'base64');

    try {
      const wb = XLSX.read(buffer, { type: 'buffer' });
      const rows = XLSX.utils.sheet_to_json(wb.Sheets[wb.SheetNames[0]], {
        header: 1,
        raw: false
      });

      if (rows.length < 4) throw new Error('Template too short');
      console.log(`📊  Parsed XLSX with ${rows.length} rows`);

      const weightRow = rows[1], headerRow = rows[2], dataRows = rows.slice(3);

      const map = {
        'Αριθμός Μητρώου': 'AM',
        'Ονοματεπώνυμο': 'name',
        'Ακαδημαϊκό E-mail': 'email',
        'Περίοδος δήλωσης': 'declarationPeriod',
        'Τμήμα Τάξης': 'classTitle',
        'Κλίμακα βαθμολόγησης': 'gradingScale',
        'Βαθμολογία': 'grade'
      };

      let totalInserted = 0;

      for (const row of dataRows) {
        const d = {};
        headerRow.forEach((t, i) => {
          const k = map[t?.trim()];
          if (k && row[i] != null && row[i] !== '')
            d[k] = k === 'grade' ? parseFloat(row[i]) : row[i].toString().trim();
        });

        for (let q = 1; q <= 10; q++) {
          const idx = 8 + (q - 1);
          const score = parseFloat(row[idx]);
          const weight = parseFloat(weightRow[idx]);
          d[`Q${q}`] = (!isNaN(score) && !isNaN(weight)) ? score * weight : null;
        }

        const now = new Date();

        // Upsert grading
        const gradingSql = `
          INSERT INTO grading (
            AM, name, email, declarationPeriod, classTitle,
            gradingScale, grade,
            Q1, Q2, Q3, Q4, Q5, Q6, Q7, Q8, Q9, Q10
          ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
          ON DUPLICATE KEY UPDATE
            name = VALUES(name),
            email = VALUES(email),
            gradingScale = VALUES(gradingScale),
            grade = VALUES(grade),
            Q1 = VALUES(Q1), Q2 = VALUES(Q2), Q3 = VALUES(Q3), Q4 = VALUES(Q4), Q5 = VALUES(Q5),
            Q6 = VALUES(Q6), Q7 = VALUES(Q7), Q8 = VALUES(Q8), Q9 = VALUES(Q9), Q10 = VALUES(Q10)
        `;

        await connection.execute(gradingSql, [
          d.AM, d.name, d.email, d.declarationPeriod, d.classTitle,
          d.gradingScale, d.grade,
          d.Q1, d.Q2, d.Q3, d.Q4, d.Q5, d.Q6, d.Q7, d.Q8, d.Q9, d.Q10
        ]);

          if (totalInserted==0) {
          // Submission log handling (without AM)
          const [existingLog] = await connection.execute(
            `SELECT initialSubmissionDate, finalSubmissionDate 
            FROM submission_log 
            WHERE declarationPeriod = ? AND classTitle = ?`,
            [d.declarationPeriod, d.classTitle]
          );
          if (existingLog.length === 0) {
            await connection.execute(
              `INSERT INTO submission_log (declarationPeriod, classTitle, initialSubmissionDate)
              VALUES (?, ?, ?)`,
              [d.declarationPeriod, d.classTitle, now]
            );
          } else {
            await connection.execute(
              `UPDATE submission_log SET finalSubmissionDate = ? 
              WHERE declarationPeriod = ? AND classTitle = ?`,
              [now, d.declarationPeriod, d.classTitle]
            );
          }
        }
        totalInserted++;

      }
      

      console.log(`✅  Processed ${totalInserted} grades`);
      reply(rpcOK({ message: `Processed ${totalInserted} grades` }));
      channel.ack(msg);

    } catch (err) {
      console.error('❌ Error processing grades:', err.message);
      const invalid = err.message === 'Template too short';
      reply(rpcError(invalid ? 'INVALID_REQUEST' : 'INTERNAL_ERROR', invalid ? 'Invalid grade workbook' : 'Could not process grades', !invalid));
      channel.nack(msg, false, false);
    }
  }, { noAck: false });

// -- histogram helper with dynamic upper‐bound on bins --
async function fetchHistogram(field, connection, { classTitle, declarationPeriod }) {
  // round the float into integer bins
  const sql = `
    SELECT
      ROUND(\`${field}\`) AS value,
      COUNT(*)           AS count
    FROM grading
    WHERE classTitle       = ?
      AND declarationPeriod = ?
    GROUP BY ROUND(\`${field}\`)
    ORDER BY ROUND(\`${field}\`)
  `;
  const [rows] = await connection.execute(sql, [ classTitle, declarationPeriod ]);

  // if it's the overall 'grade', force 0–10; otherwise use max rounded value
  const maxBin = field === 'grade'
    ? 10
    : rows.reduce((max, r) => Math.max(max, +r.value), 0);

  // build bins 0 through maxBin
  const categories = Array.from({ length: maxBin + 1 }, (_, i) => i);

  // map counts into those bins
  const data = categories.map(i => {
    const found = rows.find(r => +r.value === i);
    return found ? +found.count : 0;
  });

  return { categories, data };
}



})();
