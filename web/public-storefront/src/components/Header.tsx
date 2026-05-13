import Image from "next/image";

export function Header() {
  return (
    <header className="border-b border-stroke bg-bg01">
      <div className="max-w-3xl mx-auto px-4 py-4 flex items-center gap-3">
        <Image
          src="/obol-stack-logo.png"
          alt="Obol Stack"
          width={161}
          height={28}
          priority
          className="h-7 w-auto"
        />
      </div>
    </header>
  );
}
