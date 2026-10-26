FROM node:22-alpine
WORKDIR /app
COPY server.mjs passctl.mjs ./
ENV DATA_DIR=/data
USER node
EXPOSE 8787
CMD ["node", "server.mjs"]
