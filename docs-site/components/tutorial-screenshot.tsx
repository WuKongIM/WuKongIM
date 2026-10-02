import Image from 'next/image';
import type { ReactNode } from 'react';

interface TutorialScreenshotProps {
  src: string;
  alt: string;
  width: number;
  height: number;
  /** Percentage coordinates keep numbered callouts aligned with the original capture. */
  marks?: readonly { x: number; y: number }[];
  children: ReactNode;
}

/** Presents an original screenshot with translatable captions and a link to its full size. */
export function TutorialScreenshot({ src, alt, width, height, marks = [], children }: TutorialScreenshotProps) {
  return (
    <figure className="tutorial-screenshot not-prose" style={{ maxWidth: width }}>
      <a className="tutorial-screenshot-image" href={src} target="_blank" rel="noopener noreferrer" aria-label={alt}>
        <Image src={src} alt={alt} width={width} height={height} sizes="(max-width: 768px) 100vw, 900px" />
        {marks.map((mark, index) => (
          <span key={index} className="tutorial-screenshot-mark" style={{ left: `${mark.x}%`, top: `${mark.y}%` }} aria-hidden="true">
            {index + 1}
          </span>
        ))}
      </a>
      <figcaption>{children}</figcaption>
    </figure>
  );
}
