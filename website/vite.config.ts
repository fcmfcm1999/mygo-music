import { defineConfig } from 'vite'
import { tanstackStart } from '@tanstack/solid-start/plugin/vite'
import solid from 'vite-plugin-solid'
import { pluginQuery, pluginsFrom } from './src/plugins'

// The latest release, with its installers, for the download page to link
// to. It's fetched once, as the site is built: a release that is published
// later is linked to once the site is built again. GH_TOKEN or GITHUB_TOKEN,
// if set, is sent with the request, for the API's rate limit.
const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN
const github = (path: string) =>
  fetch(`https://api.github.com${path}`, { headers: token ? { authorization: `Bearer ${token}` } : {} })

async function latestRelease() {
  const res = await github('/repos/egoist/mygo-music/releases/latest')
  if (!res.ok) throw new Error(`fetching the latest release: GitHub answered ${res.status}`)
  const r = await res.json()
  return {
    tag_name: r.tag_name,
    published_at: r.published_at,
    assets: r.assets.map((a: any) => ({ name: a.name, size: a.size, browser_download_url: a.browser_download_url })),
  }
}

// The plugins on GitHub, by their topic, for the plugins page to list as
// it's built. The page asks GitHub again in the browser, for the plugins
// published since.
async function plugins() {
  const res = await github(`/search/repositories?q=${encodeURIComponent(pluginQuery)}&sort=stars&per_page=100`)
  if (!res.ok) throw new Error(`searching for plugins: GitHub answered ${res.status}`)
  return pluginsFrom(await res.json())
}

export default defineConfig(async () => ({
  define: {
    __RELEASE__: JSON.stringify(await latestRelease()),
    __PLUGINS__: JSON.stringify(await plugins()),
  },
  plugins: [
    // Every page is rendered at build time, so the site is plain files
    // that Cloudflare serves as static assets.
    tanstackStart({
      prerender: {
        enabled: true,
        crawlLinks: true,
        failOnError: true,
        // Pages are fetched from a local preview server, which now and then
        // resets a connection on Cloudflare's builders (fetch failed:
        // ECONNRESET). A reset page is fetched again rather than failing
        // the build, and fewer pages are asked for at once: by default it's
        // one per CPU, and the builder reports many.
        concurrency: 4,
        retryCount: 3,
        retryDelay: 1000,
        // Links to a section of a page are that page.
        filter: ({ path }) => !path.includes('#'),
      },
      // Cloudflare serves 404.html for paths that have no file.
      pages: [{ path: '/404', prerender: { autoSubfolderIndex: false } }],
    }),
    solid({ ssr: true }),
  ],
}))
