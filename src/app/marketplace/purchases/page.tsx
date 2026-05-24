import type { Metadata } from 'next';

import { MyPurchasesPage } from '@/components/marketplace/MyPurchasesPage';

export const metadata: Metadata = {
  title: 'My Purchases · Obol Stack',
};

export default function Page() {
  return <MyPurchasesPage />;
}
