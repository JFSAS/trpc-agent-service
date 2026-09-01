# syntax=docker/dockerfile:1.7
ARG NODE_IMAGE=node:20-alpine
FROM ${NODE_IMAGE} AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM ${NODE_IMAGE} AS build
WORKDIR /app
ARG RUNTIME_API_BASE=http://durable-gateway:8080
ARG NEXT_PUBLIC_TENANT_ID=tenant-control-smoke
ARG NEXT_PUBLIC_AGENT_APP_ID=agent-commerce-01
ENV RUNTIME_API_BASE=$RUNTIME_API_BASE
ENV NEXT_PUBLIC_TENANT_ID=$NEXT_PUBLIC_TENANT_ID
ENV NEXT_PUBLIC_AGENT_APP_ID=$NEXT_PUBLIC_AGENT_APP_ID
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN npm run build

FROM ${NODE_IMAGE} AS runtime
WORKDIR /app
ENV NODE_ENV=production HOSTNAME=0.0.0.0 PORT=3000
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
EXPOSE 3000
USER node
CMD ["node", "server.js"]
