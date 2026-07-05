import type { MetadataRoute } from "next";

// The PWA manifest makes EscrowPay installable on a phone home screen, which is
// how the product is demoed. Icons reuse the app's SVG mark rendered at install
// sizes.
export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "EscrowPay — bank-native micro-escrow",
    short_name: "EscrowPay",
    description:
      "Buyer-protected delivery for social-commerce payments. Funds are held by the bank until the buyer confirms the handoff.",
    start_url: "/",
    display: "standalone",
    background_color: "#f6f7f9",
    theme_color: "#059669",
    icons: [
      { src: "/icon.svg", sizes: "any", type: "image/svg+xml", purpose: "any" },
      { src: "/icon-192.png", sizes: "192x192", type: "image/png", purpose: "any" },
      { src: "/icon-512.png", sizes: "512x512", type: "image/png", purpose: "any" },
      { src: "/icon-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
      { src: "/favicon.ico", sizes: "48x48", type: "image/x-icon" },
    ],
  };
}
