import { StorefrontPreview } from "@/components/StorefrontPreview";
import { fetchCatalogDocument } from "@/lib/catalog";
export const dynamic = "force-dynamic";
export const revalidate = 0;

export default async function Home() {
  const catalog = await fetchCatalogDocument();
  return <StorefrontPreview initial={catalog} />;
}
