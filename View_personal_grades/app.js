require('dotenv').config();
const amqp = require('amqplib');
const mysql = require('mysql2/promise');
const XLSX = require('xlsx');

const rpcOK = data => ({ version: 1, data });
const rpcError = (code, message, retryable = false) => ({
  version: 1,
  error: { code, message, retryable }
});

const {
  MYSQL_URI,
  RABBITMQ_URI,
  RABBITMQ_EXCHANGE,
  RABBITMQ_ROUTING_KEY,
  RABBITMQ_GET_GRADES_KEY
} = process.env;

const missingVars = [];
if (!MYSQL_URI) missingVars.push("MYSQL_URI");
if (!RABBITMQ_URI) missingVars.push("RABBITMQ_URI");
if (!RABBITMQ_EXCHANGE) missingVars.push("RABBITMQ_EXCHANGE");
if (!RABBITMQ_ROUTING_KEY) missingVars.push("RABBITMQ_ROUTING_KEY");
if (!RABBITMQ_GET_GRADES_KEY) missingVars.push("RABBITMQ_GET_GRADES_KEY");

if (missingVars.length > 0) {
  console.error(`[${new Date().toISOString()}] ❌ Missing required environment variables: ${missingVars.join(", ")}`);
  process.exit(1);
}

const log = (...args) => console.log(`[${new Date().toISOString()}]`, ...args);

(async () => {
  // Connect to MySQL
  let connection;
  try {
    connection = await mysql.createConnection(MYSQL_URI);
    log('✅ Connected to MySQL');
  } catch (err) {
    log('❌ Failed to connect to MySQL:', err.message);
    process.exit(1);
  }

  // Connect to RabbitMQ & exchange
  let conn, channel;
  try {
    conn = await amqp.connect(RABBITMQ_URI);
    channel = await conn.createChannel();
    await channel.assertExchange(RABBITMQ_EXCHANGE, 'direct', { durable: true });
    await channel.assertExchange('clearsky.dlx.v1', 'direct', { durable: true });
    log('✅ Connected to RabbitMQ and exchange set');
  } catch (err) {
    log('❌ RabbitMQ connection/setup failed:', err.message);
    process.exit(1);
  }

  // Helper to reply on the same correlationId & replyTo
  const makeReply = msg => payload => {
    const { replyTo, correlationId } = msg.properties;
    if (!replyTo) {
      log('⚠️ No replyTo queue specified; skipping reply');
      return;
    }
    try {
      channel.publish('', replyTo, Buffer.from(JSON.stringify(payload)), {
        contentType: 'application/json',
        correlationId
      });
      log(`📤 Reply sent (corrId=${correlationId}) to ${replyTo}`);
    } catch (err) {
      log('❌ Failed to send reply:', err.message);
    }
  };

  const handleGradeLookup = async msg => {
    const reply = makeReply(msg);
    try {
      const body = JSON.parse(msg.content.toString());
      const am = ((body.AM || body.student_id) || '').trim();
      if (!am) throw new Error('INVALID_STUDENT_ID');
      const [rows] = await connection.execute(
        'SELECT declarationPeriod, classTitle, grading_status, grade FROM grading WHERE AM = ?', [am]
      );
      reply(rpcOK(rows));
      channel.ack(msg);
    } catch (err) {
      const invalid = err.message === 'INVALID_STUDENT_ID' || err instanceof SyntaxError;
      reply(rpcError(invalid ? 'INVALID_REQUEST' : 'DEPENDENCY_UNAVAILABLE', invalid ? 'Student ID is required' : 'Grade store is unavailable', !invalid));
      channel.nack(msg, false, false);
    }
  };

  // ───────────────────────────────────────────────────────────────────────────
  // 1️⃣ Grade Import via XLSX
  const importQueue = 'clearsky.personal-grades.commands.v1';
  await channel.assertQueue(importQueue, { durable: true, arguments: { 'x-dead-letter-exchange': 'clearsky.dlx.v1', 'x-dead-letter-routing-key': 'clearsky.personal-grades.commands.v1.dead' } });
  await channel.assertQueue('clearsky.personal-grades.commands.v1.dlq', { durable: true });
  await channel.bindQueue('clearsky.personal-grades.commands.v1.dlq', 'clearsky.dlx.v1', 'clearsky.personal-grades.commands.v1.dead');
  await channel.bindQueue(importQueue, RABBITMQ_EXCHANGE, RABBITMQ_ROUTING_KEY);
  await channel.bindQueue(importQueue, RABBITMQ_EXCHANGE, RABBITMQ_GET_GRADES_KEY);
  channel.prefetch(10);
  log(`🚀 Listening for XLSX uploads on "${RABBITMQ_ROUTING_KEY}"`);

  channel.consume(importQueue, async msg => {
    if (!msg) return;
    if (msg.fields.routingKey === RABBITMQ_GET_GRADES_KEY) return handleGradeLookup(msg);
    log('📩 Received XLSX grade message');
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
      log(`📊 Parsed XLSX with ${rows.length} rows`);

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

        const gradingSql = `
          INSERT INTO grading (
            AM, name, email, declarationPeriod, classTitle,
            gradingScale, grade,
            Q1, Q2, Q3, Q4, Q5, Q6, Q7, Q8, Q9, Q10, grading_status
          ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
          ON DUPLICATE KEY UPDATE
            name = VALUES(name),
            email = VALUES(email),
            gradingScale = VALUES(gradingScale),
            grade = VALUES(grade),
            Q1 = VALUES(Q1), Q2 = VALUES(Q2), Q3 = VALUES(Q3), Q4 = VALUES(Q4), Q5 = VALUES(Q5),
            Q6 = VALUES(Q6), Q7 = VALUES(Q7), Q8 = VALUES(Q8), Q9 = VALUES(Q9), Q10 = VALUES(Q10),
            grading_status = 1
        `;

        await connection.execute(gradingSql, [
          d.AM, d.name, d.email, d.declarationPeriod, d.classTitle,
          d.gradingScale, d.grade,
          d.Q1, d.Q2, d.Q3, d.Q4, d.Q5, d.Q6, d.Q7, d.Q8, d.Q9, d.Q10
        ]);

        log(`✅ Upserted grade for AM=${d.AM}, class=${d.classTitle}`);
        totalInserted++;
      }

      log(`🎉 Imported total of ${totalInserted} grades`);
      reply(rpcOK({ message: `Processed ${totalInserted} grades` }));
      channel.ack(msg);
    } catch (err) {
      log('❌ Error importing grades:', err.message);
      const invalid = err.message === 'Template too short';
      reply(rpcError(invalid ? 'INVALID_REQUEST' : 'INTERNAL_ERROR', invalid ? 'Invalid grade workbook' : 'Could not import grades', !invalid));
      channel.nack(msg, false, false);
    }
  }, { noAck: false });


})();
