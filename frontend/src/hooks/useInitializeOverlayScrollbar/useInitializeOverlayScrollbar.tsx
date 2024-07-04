import { useIsDarkMode } from 'hooks/useDarkMode';
import { useOverlayScrollbars } from 'overlayscrollbars-react';
import {
	Dispatch,
	RefObject,
	SetStateAction,
	useEffect,
	useRef,
	useState,
} from 'react';

const useInitializeOverlayScrollbar = (): {
	setScroller: Dispatch<SetStateAction<null>>;
	rootRef: RefObject<HTMLDivElement>;
} => {
	const rootRef = useRef(null);
	const isDarkMode = useIsDarkMode();
	const [scroller, setScroller] = useState(null);
	const [initialize, osInstance] = useOverlayScrollbars({
		defer: true,
		options: {
			scrollbars: {
				autoHide: 'scroll',
				theme: isDarkMode ? 'os-theme-light' : 'os-theme-dark',
			},
		},
	});

	useEffect(() => {
		const { current: root } = rootRef;

		if (scroller && root) {
			initialize({
				target: root,
				elements: {
					viewport: scroller,
				},
			});
		}

		return (): void => osInstance()?.destroy();
	}, [scroller, initialize, osInstance]);

	return { setScroller, rootRef };
};

export default useInitializeOverlayScrollbar;
