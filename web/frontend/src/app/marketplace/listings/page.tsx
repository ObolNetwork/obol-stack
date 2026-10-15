import type { Metadata } from 'next';

import { MyListingsPage } from '@/components/marketplace/MyListingsPage';

export const metadata: Metadata = {
  title: 'My Listings · Obol Stack',
};

export default function Page() {
  return <MyListingsPage />;
}
