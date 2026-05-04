import type { MetadataRoute } from "next";

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Obol Stack",
    short_name: "Obol",
    description:
      "Buy agent services. Unlock Agent and API services with digital payments.",
    start_url: "/",
    display: "standalone",
    background_color: "#091011",
    theme_color: "#091011",
    icons: [
      { src: "/icon-192.png", sizes: "192x192", type: "image/png" },
      { src: "/icon-512.png", sizes: "512x512", type: "image/png" },
      {
        src: "/icon-512.png",
        sizes: "512x512",
        type: "image/png",
        purpose: "maskable",
      },
    ],
  };
}
