require('dotenv').config();

require('./mongodb/schema'); 
require('./mongodb/init'); 
require('./mongodb/create'); 



const amqp           = require('amqplib');
const mongoose       = require('mongoose');
const { MongoClient } = require('mongodb');
const XLSX           = require('xlsx');

const rpcOK = data => ({ version: 1, data });
const rpcError = (code, message, retryable = false) => ({
  version: 1,
  error: { code, message, retryable }
});

/* const {
  MONGO_URI,
  RABBITMQ_URI,
  RABBITMQ_EXCHANGE,
  RABBITMQ_ROUTING_KEY,
  RABBITMQ_CREDIT_INCR_KEY
} = process.env;

if (!MONGO_URI || !RABBITMQ_URI || !RABBITMQ_EXCHANGE ||
    !RABBITMQ_ROUTING_KEY || !RABBITMQ_CREDIT_INCR_KEY) {
  console.error('❌  Missing required environment variables');
  process.exit(1);
} */

const {
  MONGO_URI,
  RABBITMQ_URI,
  RABBITMQ_EXCHANGE,
  RABBITMQ_ROUTING_KEY,
  RABBITMQ_CREDIT_INCR_KEY
} = process.env;

const missingVars = [];

if (!MONGO_URI) missingVars.push("MONGO_URI");
if (!RABBITMQ_URI) missingVars.push("RABBITMQ_URI");
if (!RABBITMQ_EXCHANGE) missingVars.push("RABBITMQ_EXCHANGE");
if (!RABBITMQ_ROUTING_KEY) missingVars.push("RABBITMQ_ROUTING_KEY");
if (!RABBITMQ_CREDIT_INCR_KEY) missingVars.push("RABBITMQ_CREDIT_INCR_KEY");

if (missingVars.length > 0) {
  console.error(`❌ Missing required environment variables: ${missingVars.join(", ")}`);
  process.exit(1);
}

(async () => {
  // Connect to MongoDB for grades
  try {
    await mongoose.connect(MONGO_URI);
    console.log('✅  Connected to MongoDB via mongoose (grades)');
  } catch (err) {
    console.error('❌ Failed to connect to MongoDB (grades):', err.message);
    process.exit(1);
  }

  // Connect to MongoDB for credits
  let creditsClient;
  try {
    creditsClient = new MongoClient(MONGO_URI);
    await creditsClient.connect();
    console.log('✅  Connected to MongoDB via MongoClient (credits)');
  } catch (err) {
    console.error('❌ Failed to connect to MongoDB (credits):', err.message);
    process.exit(1);
  }

  const creditsDb = creditsClient.db('final_grades');
  const creditsColl = creditsDb.collection('credits');

  const Grade = mongoose.model('Grade', new mongoose.Schema({
    AM: String, name: String, email: String,
    declarationPeriod: String, classTitle: String,
    gradingScale: String, grade: Number,
    Q1: Number, Q2: Number, Q3: Number, Q4: Number,
    Q5: Number, Q6: Number, Q7: Number, Q8: Number,
    Q9: Number, Q10: Number
  }));

  // Connect to RabbitMQ
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
      channel.publish(
        '', replyTo,
        Buffer.from(JSON.stringify(payload)),
        { contentType: 'application/json', correlationId }
      );
      console.log(`📤  Reply sent to ${replyTo} (corrId: ${correlationId})`);
    } catch (err) {
      console.error('❌ Failed to send reply:', err.message);
    }
  };

  // ─── Listener 1: Grades
  {
    const q1 = await channel.assertQueue('clearsky.final-grades.commands.v1', { durable: true, arguments: { 'x-dead-letter-exchange': 'clearsky.dlx.v1', 'x-dead-letter-routing-key': 'clearsky.final-grades.commands.v1.dead' } });
    await channel.assertQueue('clearsky.final-grades.commands.v1.dlq', { durable: true });
    await channel.bindQueue('clearsky.final-grades.commands.v1.dlq', 'clearsky.dlx.v1', 'clearsky.final-grades.commands.v1.dead');
    await channel.bindQueue(q1.queue, RABBITMQ_EXCHANGE, RABBITMQ_ROUTING_KEY);
    await channel.bindQueue(q1.queue, RABBITMQ_EXCHANGE, RABBITMQ_CREDIT_INCR_KEY);
    channel.prefetch(10);
    console.log(`🚀  Listening for grades on "${RABBITMQ_ROUTING_KEY}"`);

    channel.consume(q1.queue, async msg => {
      if (!msg) return;
      const reply = makeReply(msg);
      if (msg.fields.routingKey === RABBITMQ_CREDIT_INCR_KEY) {
        try {
          const { name, amount } = JSON.parse(msg.content.toString());
          if (typeof name !== 'string' || !name.trim() || !Number.isFinite(amount) || amount <= 0) throw new Error('INVALID_CREDIT_PAYLOAD');
          const existing = await creditsColl.findOne({ name });
          if (!existing) await creditsColl.insertOne({ name, cred: 50 + amount });
          else await creditsColl.updateOne({ name }, { $inc: { cred: amount } });
          reply(rpcOK({ message: 'Credits updated' }));
          channel.ack(msg);
        } catch (err) {
          const invalid = err.message === 'INVALID_CREDIT_PAYLOAD';
          reply(rpcError(invalid ? 'INVALID_REQUEST' : 'INTERNAL_ERROR', invalid ? 'A valid institution and positive amount are required' : 'Could not update credits', !invalid));
          channel.nack(msg, false, false);
        }
        return;
      }
      console.log('📩  Received grade message');

      const ct = (msg.properties.contentType || '').toLowerCase().trim();
      const buffer = (ct.includes('spreadsheet') || ct === 'application/octet-stream')
        ? msg.content
        : Buffer.from(msg.content.toString(), 'base64');

      try {
        const wb   = XLSX.read(buffer, { type: 'buffer' });
        const rows = XLSX.utils.sheet_to_json(wb.Sheets[wb.SheetNames[0]], {
          header: 1, raw: false
        });
        console.log(`📊  Parsed XLSX with ${rows.length} rows`);
        if (rows.length < 4) throw new Error('Template too short');

        const weightRow = rows[1], headerRow = rows[2], dataRows = rows.slice(3);
        const map = {
          'Αριθμός Μητρώου':'AM', 'Ονοματεπώνυμο':'name',
          'Ακαδημαϊκό E-mail':'email','Περίοδος δήλωσης':'declarationPeriod',
          'Τμήμα Τάξης':'classTitle','Κλίμακα βαθμολόγησης':'gradingScale',
          'Βαθμολογία':'grade'
        };

        const docs = dataRows.map(row => {
          const d = {};
          headerRow.forEach((t,i) => {
            const k = map[t?.trim()];
            if (!k) return;
            if (row[i] != null && row[i] !== '')
              d[k] = k === 'grade'
                     ? parseFloat(row[i])
                     : row[i].toString().trim();
          });
          for (let q = 1; q <= 10; q++) {
            const idx    = 8 + (q - 1);
            const score  = parseFloat(row[idx]);
            const weight = parseFloat(weightRow[idx]);
            d[`Q${q}`] = (!isNaN(score) && !isNaN(weight))
              ? score * weight
              : null;
          }
          return d;
        });

        const firstAM = docs[0]?.AM || '';
        let org = null;
        if (firstAM.startsWith('031')) org = 'NTUA';
        else if (firstAM.startsWith('022')) org = 'EKPA';

        if (org) {
          const upd = await creditsColl.updateOne(
            { name: org }, { $inc: { cred: -1 } }
          );
          console.log(upd.matchedCount
            ? `➖  Decremented credit for ${org}`
            : `⚠️  No credit document for ${org}`);
        } else {
          console.warn('⚠️  Unknown organization for AM:', firstAM);
        }

        // const upd = await creditsColl.updateOne(
        //     { name: "NTUA" }, { $inc: { cred: -1 } })
        const res = await Grade.insertMany(docs, { ordered: false });
        console.log(`✅  Inserted ${res.length} grades`);
        reply(rpcOK({ message: `Inserted ${res.length}` }));
        channel.ack(msg);

      } catch (err) {
        console.error('❌ Error processing grades:', err.message);
        const invalid = err.message === 'Template too short';
        reply(rpcError(invalid ? 'INVALID_REQUEST' : 'INTERNAL_ERROR', invalid ? 'Invalid grade workbook' : 'Could not import grades', !invalid));
        channel.nack(msg, false, false);
      }
    }, { noAck: false });
  }

  // `incr.credits` is handled by the same owned command queue as final uploads.
  // Keeping dispatch in one consumer prevents RabbitMQ from load-balancing unrelated
  // work between separate consumers of a shared queue.

})();
