const express = require('express');
const app = express();
const router = express.Router();
const port = 3000;

// url: http://localhost:3000/
app.get('/', (request, response) => response.json({api: "online"}));

// all routes prefixed with /api
app.use('/api', router);

// using router.get() to prefix our path
// url: http://localhost:3000/api/
router.get('/', (request, response) => {
  response.json({session: generateSessionID(), cookie: generateCookieID()});
});

router.get('/page', (request, response) => {
    response.json({page: generatePageID()});
  });

app.get('/shutdown', async (request, response) => {
    console.log(`${Date.now().toString()} | Initiating shutdown and closing port ${port}`)
    response.set("Connection", "close");
    server.close();
    process.exit();
})

var server = app.listen(port, () => console.log(`${Date.now().toString()} | Listening on port ${port}`));

function generateCookieID() {
    "use strict";
    return Math.random().toString(36).substr(2, 15) + Math.random().toString(36).substr(2, 15)
};

function generateSessionID() {
    var uid = [], i;
    for (i = 0; i < 6; i++) uid.push(Math.random().toString(16).substr(2,8));
    return uid.join('-');
};

function generatePageID() {
    "use strict";
    return Math.random().toString(36).substr(3, 6)
};
