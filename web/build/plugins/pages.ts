import Pages from 'vite-plugin-pages';

export default function createPage() {
  return Pages({
    dirs: 'src/pages',
    exclude: ['**/components/*.vue']
  });
}