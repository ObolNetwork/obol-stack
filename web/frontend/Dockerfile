# -------------------------------
# Base image
# -------------------------------
FROM node:24-alpine AS base

# Install a pinned pnpm via corepack so Docker builds match CI and local lockfile behavior.
RUN npm install -g --force corepack \
    && corepack enable \
    && corepack prepare pnpm@10.30.1 --activate

WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1

# -------------------------------
# Dependencies layer (cached)
# -------------------------------
FROM base AS deps

# Needed for some native modules
RUN apk add --no-cache libc6-compat

# Copy only package files for caching
COPY package.json pnpm-lock.yaml* ./

# Install dependencies deterministically from the checked-in lockfile.
RUN pnpm install --frozen-lockfile

# -------------------------------
# Builder
# -------------------------------
FROM base AS builder

WORKDIR /app

# Copy node_modules from deps (layer caching)
COPY --from=deps /app/node_modules ./node_modules

# Copy the rest of the app
COPY . .

# Build args for metadata
ARG VERSION
ARG BUILDTIME
ARG GITCOMMIT
ARG NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID
ENV NEXT_PUBLIC_VERSION=$VERSION
ENV NEXT_PUBLIC_BUILDTIME=$BUILDTIME
ENV NEXT_PUBLIC_GITCOMMIT=$GITCOMMIT
ENV NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID=$NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID

# Build the Next.js app
RUN pnpm run build

# -------------------------------
# Production runner
# -------------------------------
FROM base AS runner

WORKDIR /app

# Add non-root user
RUN addgroup --system --gid 1001 nodejs && \
    adduser --system --uid 1001 nextjs

# Copy only the necessary build output, set ownership directly
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static
COPY --from=builder --chown=nextjs:nodejs /app/public ./public

# No source maps in the image: they publish the server source and nothing
# reads them at runtime.
RUN find . -name '*.map' -type f -delete

# Ensure ownership recursively (in case any files/folders are missing ownership)
RUN chown -R nextjs:nodejs .next ./.next/static ./public

USER nextjs

EXPOSE 3000
ENV PORT=3000
ENV HOSTNAME="0.0.0.0"
ENV NEXT_PUBLIC_HOSTNAME="0.0.0.0"

# Run the Next.js standalone server
CMD ["node", "server.js"]
