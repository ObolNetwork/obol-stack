export default {
  async fetch(request) {
    const url = new URL(request.url);
    
    // Check if the request is from a browser
    // Redirect browsers to the GitHub repository for better UX
    const userAgent = request.headers.get('User-Agent') || '';
    const isBrowser = userAgent.includes('Mozilla') && !userAgent.includes('curl') && !userAgent.includes('wget');

    if (isBrowser && url.pathname === '/') {
      return Response.redirect('https://github.com/ObolNetwork/obol-stack', 302);
    }

    // Determine release from path or query param
    // 1. Path: https://stack.obol.org/v1.0.0
    // 2. Query: https://stack.obol.org?release=v1.0.0
    // 3. Default: main
    let release = 'main';
    const pathSegment = url.pathname.slice(1); // Remove leading slash
    
    if (pathSegment && pathSegment !== '') {
        release = pathSegment;
    } else if (url.searchParams.get('release')) {
        release = url.searchParams.get('release');
    }

    const githubUrl = `https://raw.githubusercontent.com/ObolNetwork/obol-stack/${release}/obolup.sh`;

    const response = await fetch(githubUrl);

    if (!response.ok) {
        return new Response(`Failed to fetch installer for release: ${release}`, { status: 404 });
    }

    let scriptContent = await response.text();

    // If a specific release is requested (not main), inject the environment variable
    // so that piping to bash installs that specific version automatically.
    if (release !== 'main') {
        const injection = `export OBOL_RELEASE="${release}"\n`;
        
        if (scriptContent.startsWith('#!')) {
            // Insert after the first line (shebang)
            const firstNewline = scriptContent.indexOf('\n');
            if (firstNewline !== -1) {
                scriptContent = scriptContent.slice(0, firstNewline + 1) + injection + scriptContent.slice(firstNewline + 1);
            } else {
                // File has only one line? Append.
                scriptContent = scriptContent + '\n' + injection;
            }
        } else {
            // No shebang, prepend
            scriptContent = injection + scriptContent;
        }
    }

    return new Response(scriptContent, {
      status: response.status,
      headers: {
        'Content-Type': 'text/plain; charset=utf-8',
        'Cache-Control': 'no-cache, no-store, must-revalidate',
      },
    });
  },
};
