// front-end/app.js
import express           from 'express';
import session           from 'express-session';
import cookieParser      from 'cookie-parser'; // Add cookie parser import
import path              from 'path';
import { fileURLToPath } from 'url';
import morgan            from 'morgan';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const app       = express();

const SESSION_SECRET = process.env.SESSION_SECRET;
if (!SESSION_SECRET || SESSION_SECRET.length < 32) {
  throw new Error('SESSION_SECRET must contain at least 32 characters');
}

// ─────────────────────────────────────────────────────────────────────────────
// 0)  API base-URL resolution
// ─────────────────────────────────────────────────────────────────────────────
const API_BASE        =
  process.env.GO_API_URL       ||
  process.env.ORCHESTRATOR_URL ||
  'http://orchestrator:8080';

// ─────────────────────────────────────────────────────────────────────────────
// 1)  3rd-party middleware
// ─────────────────────────────────────────────────────────────────────────────
app.use(morgan('dev'));
app.use(cookieParser()); // Add cookie parser middleware

// ─────────────────────────────────────────────────────────────────────────────
// 2)  Static assets
// ─────────────────────────────────────────────────────────────────────────────
app.use(express.static(path.join(__dirname, 'public')));

// ─────────────────────────────────────────────────────────────────────────────
// 3)  Body-parsers
// ─────────────────────────────────────────────────────────────────────────────
app.use(express.urlencoded({ extended: false }));
app.use(express.json());

// ─────────────────────────────────────────────────────────────────────────────
// 4)  Sessions & locals
// ─────────────────────────────────────────────────────────────────────────────
app.use(session({
  secret           : SESSION_SECRET,
  resave           : false,
  saveUninitialized: false,
  cookie           : {
    httpOnly: true,
    sameSite: 'lax',
    secure: process.env.COOKIE_SECURE === 'true'
  }
}));
app.use((req, res, next) => {
  res.locals.user       = req.session.user || null;
  res.locals.currentUrl = req.originalUrl;
  res.locals.API_BASE   = API_BASE;
  next();
});

// ─────────────────────────────────────────────────────────────────────────────
// 5)  EJS templating
// ─────────────────────────────────────────────────────────────────────────────
app.set('view engine', 'ejs');
app.set('views', path.join(__dirname, 'views'));

// ─────────────────────────────────────────────────────────────────────────────
// 6)  GOOGLE SIGN-IN
//    The proxy sends `/auth/google/...` to identity, which redirects back here.
// ─────────────────────────────────────────────────────────────────────────────
async function authenticatedUser(req) {
  const token = req.cookies.jwt;
  if (!token) return null;
  const response = await fetch(`${API_BASE}/user/me`, {
    headers: { Cookie: `jwt=${encodeURIComponent(token)}` }
  });
  if (!response.ok) return null;
  const payload = await response.json();
  return payload.data || null;
}

// Identity (behind the proxy at /auth/google/*) redirects here after Google
// sign-in has set the session cookie.
app.get('/login/google/success', async (req, res) => {
  try {
    const user = await authenticatedUser(req);
    if (!user) return res.redirect('/login?error=google_login_failed');
    req.session.user = { username: user.username, role: user.role };

    // Redirect based on role
    let redirectPath = '/';
    switch (user.role) {
      case 'student':
        redirectPath = '/student';
        break;
      case 'instructor':
        redirectPath = '/instructor';
        break;
      case 'institution_representative':
        redirectPath = '/institution';
        break;
      default:
        redirectPath = '/';
    }
    
    return res.redirect(redirectPath);
  } catch (_) {
    return res.redirect('/login?error=google_login_failed');
  }
});

// ─────────────────────────────────────────────────────────────────────────────
// 7)  Auth helper
// ─────────────────────────────────────────────────────────────────────────────
function auth(role) {
  return (req, res, next) => {
    if (!req.session.user) return res.redirect('/login');
    if (role) {
      if (role === 'institution') {
        if (!['institution','representative','institution_representative']
              .includes(req.session.user.role)) {
          return res.redirect(`/${req.session.user.role}`);
        }
      } else if (req.session.user.role !== role) {
        return res.redirect(`/${req.session.user.role}`);
      }
    }
    next();
  };
}

// ─────────────────────────────────────────────────────────────────────────────
// 8)  Dummy users (dev only)
// ─────────────────────────────────────────────────────────────────────────────
const users = { alice: 'student', bob: 'instructor', iris: 'institution' };

// ─────────────────────────────────────────────────────────────────────────────
// 9)  UI routes
// ─────────────────────────────────────────────────────────────────────────────

// Home
app.get('/', (req, res) => {
  if (!req.session.user) return res.redirect('/login');
  res.redirect(`/${req.session.user.role}`);
});

// Signup / Login
app.get('/signup', (_, res) =>
  res.render('signup', { title: 'Sign Up', user: null })
);

// Fixed messages for known reasons; the query string is never echoed.
const LOGIN_ERRORS = {
  google_domain          : 'Sign in with your university Google account.',
  google_not_registered  : 'This Google account is not registered. Students must be in the secretariat\'s registry; instructors are registered by the secretariat.',
  google_account_conflict: 'An account already exists for this student ID. Sign in with your password.',
  google_login_failed    : 'Google sign-in failed. Please try again.',
  google_unavailable     : 'Google sign-in is not available here. Sign in with your password.'
};

app.get('/login', (req, res) =>
  res.render('login', {
    title : 'Log in',
    error : LOGIN_ERRORS[req.query.error] || null,
    notice: req.query.activated === '1' ? 'Your password is set. You can now log in.' : null,
    user  : null
  })
);

app.get('/forgot-password', (_, res) =>
  res.render('forgotPassword', { title: 'Reset your password', user: null })
);

// Emailed links: the token stays in the URL fragment and is read client-side.
app.get('/activate', (_, res) =>
  res.render('activate', { title: 'Choose your password', user: null })
);

// First Google sign-in of a registry student: confirm the student ID.
app.get('/signup/google', (req, res) => {
  if (!req.cookies.google_signup) return res.redirect('/login?error=google_login_failed');
  res.render('googleSignup', { title: 'Confirm your student ID', user: null });
});

// CLASSIC form POST – creates session
app.post('/login', async (req, res) => {
  const { username, password } = req.body;
  try {
    const response = await fetch(`${API_BASE}/user/login`, {
      method : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body   : JSON.stringify({ username, password })
    });
    const payload = await response.json();
    const data = payload.data || {};

    if (!response.ok || !data.role) {
      return res.render('login', {
        title : 'Log in',
        error : payload.error?.message || 'Invalid credentials',
        notice: null,
        user  : null
      });
    }
	const sessionCookie = response.headers.get('set-cookie');
	if (sessionCookie) res.setHeader('Set-Cookie', sessionCookie);

    req.session.user = { username, role: data.role };

    if (['institution_representative','representative']
        .includes(data.role))      return res.redirect('/institution');
    else if (data.role === 'instructor') return res.redirect('/instructor');
    else if (data.role === 'student')    return res.redirect('/student');
    else                                 return res.redirect('/');
  } catch (err) {
    return res.render('login', {
      title : 'Log in',
      error : 'Login failed',
      notice: null,
      user  : null
    });
  }
});

app.post('/api/session', async (req, res) => {
  try {
    const user = await authenticatedUser(req);
    if (!user) return res.status(401).json({ error: 'Authentication required' });
    req.session.user = { username: user.username, role: user.role };
    return res.sendStatus(200);
  } catch (_) {
    return res.status(503).json({ error: 'Authentication service unavailable' });
  }
});

app.get('/logout', (req, res) =>
  req.session.destroy(() => {
    res.clearCookie('jwt', { path: '/' });
    res.redirect('/login');
  })
);

// Student UI
app.get('/student',            auth('student'), (req,res)=>res.render('student/dashboard',    { user:req.session.user, title:'Dashboard' }));
app.get('/student/statistics', auth('student'), (req,res)=>res.render('student/statistics',   { user:req.session.user, title:'Statistics' }));
app.get('/student/my-courses', auth('student'), (req,res)=>res.render('student/myCourses',    { user:req.session.user, title:'My Courses' }));
app.get('/student/request',    auth('student'), (req,res)=>res.render('student/reviewRequest',{ user:req.session.user, title:'Review Request' }));
app.get('/student/status',     auth('student'), (req,res)=>res.render('student/reviewStatus', { user:req.session.user, title:'Review Status' }));
app.get('/student/personal',   auth('student'), (req,res)=>res.render('student/personal',     { user:req.session.user, title:'Personal Grades' }));

// Instructor UI
app.get('/instructor',              auth('instructor'), (req,res)=>res.render('instructor/dashboard', { user:req.session.user, title:'Dashboard' }));
app.get('/instructor/post-initial', auth('instructor'), (req,res)=>res.render('instructor/postInitial',{ user:req.session.user, title:'Post Initial' }));
app.get('/instructor/post-final',   auth('instructor'), (req,res)=>res.render('instructor/postFinal',  { user:req.session.user, title:'Post Final' }));
app.get('/instructor/review-list',  auth('instructor'), (req,res)=>res.render('instructor/reviewList', { user:req.session.user, title:'Review Requests' }));
app.get('/instructor/reply',        auth('instructor'), (req,res)=>{
  // The details are loaded by reply.js from the request ID in the URL.
  res.render('instructor/replyForm',{
    user        : req.session.user,
    title       : 'Reply to Review Request',
    request_id  : '',
    course_name : '',
    exam_period : '',
    student_name: '',
  });
});
app.get('/instructor/statistics',   auth('instructor'), (req,res)=>res.render('instructor/statistics', { user:req.session.user, title:'Statistics' }));

// Institution UI
app.get('/institution',                 auth('institution'), (req,res)=>res.render('institution/dashboard',      { user:req.session.user, title:'Dashboard' }));
app.get('/institution/register',        auth('institution'), (req,res)=>res.render('institution/register',       { user:req.session.user, title:'Register' }));
app.get('/institution/purchase',        auth('institution'), (req,res)=>res.render('institution/purchase',       { user:req.session.user, title:'Purchase' }));
app.get('/institution/user-management', auth('institution'), (req,res)=>res.render('institution/userManagement', { user:req.session.user, title:'Users' }));
app.get('/institution/statistics',      auth('institution'), (req,res)=>res.render('institution/statistics',     { user:req.session.user, title:'Statistics' }));

app.get('/health/live', (_, res) => res.json({ status: 'live' }));

// ─────────────────────────────────────────────────────────────────────────────
// 10) Server start-up
// ─────────────────────────────────────────────────────────────────────────────
const PORT = process.env.PORT || 3000;
app.listen(PORT, () =>
  console.log(`✔ Front-end listening at http://localhost:${PORT}`)
);
