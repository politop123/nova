// Workers require a hostname for subrequests. sslip.io resolves this name to
// the Oracle VM's public IP while the stable public URL remains workers.dev.
const ORIGIN = 'http://nova.79-76-109-43.sslip.io';

export default {
  async fetch(request) {
    const incomingUrl = new URL(request.url);
    const originUrl = new URL(incomingUrl.pathname + incomingUrl.search, ORIGIN);

    const response = await fetch(new Request(originUrl, request));
    const headers = new Headers(response.headers);
    headers.set('x-nova-edge', 'cloudflare');

    return new Response(response.body, {
      status: response.status,
      statusText: response.statusText,
      headers,
    });
  },
};
