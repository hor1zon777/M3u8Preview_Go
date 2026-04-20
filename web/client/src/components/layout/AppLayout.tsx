import { Outlet } from 'react-router-dom';
import { Header } from './Header.js';
import { PageTransition } from '../ui/PageTransition.js';

export function AppLayout() {
  return (
    <div className="min-h-screen bg-emby-bg-base">
      <Header />
      <main className="px-6 py-4 lg:px-8 max-w-[1920px] mx-auto">
        <PageTransition>
          <Outlet />
        </PageTransition>
      </main>
    </div>
  );
}
