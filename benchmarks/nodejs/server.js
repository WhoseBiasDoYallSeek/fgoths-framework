const http = require('http');

// In-memory data
const users = new Map();
let nextId = 1;

// Seed data
for (let i = 1; i <= 100; i++) {
  users.set(i, {
    id: i,
    name: `User ${i}`,
    email: `user${i}@example.com`
  });
}
nextId = 101;

const server = http.createServer((req, res) => {
  const { method, url } = req;

  // Health check
  if (url === '/health') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end('{"status":"UP"}');
    return;
  }

  // List users
  if (url === '/users' && method === 'GET') {
    const userList = Array.from(users.values());
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(userList));
    return;
  }

  // Create user
  if (url === '/users' && method === 'POST') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      const user = JSON.parse(body);
      user.id = nextId++;
      users.set(user.id, user);
      res.writeHead(201, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(user));
    });
    return;
  }

  // Get user
  if (url.startsWith('/users/') && method === 'GET') {
    const id = 1; // Simplified
    const user = users.get(id);
    if (user) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(user));
    } else {
      res.writeHead(404, { 'Content-Type': 'application/json' });
      res.end('{"error":"Not found"}');
    }
    return;
  }

  res.writeHead(404);
  res.end('Not Found');
});

const port = process.env.PORT || 3000;
server.listen(port, () => {
  console.log(`🚀 Node.js Benchmark Server running on http://localhost:${port}`);
});
