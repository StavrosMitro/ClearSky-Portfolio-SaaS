# 1. Base image
FROM node:22.23.3-alpine3.24

# 2. Set working directory
WORKDIR /app

# 3. Copy dependency manifests
COPY package*.json ./

# 4. Install only production dependencies
RUN npm ci --only=production

# 5. Copy application code
COPY . .

# 6. Expose the port your app listens on
EXPOSE 3000

# 7. Start the app
CMD ["node", "app.js"]
