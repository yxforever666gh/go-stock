import {activeDrawings} from './drawing-model.js'

const fibonacciLevels = [0, 0.236, 0.382, 0.5, 0.618, 0.786, 1]

function finite(value) {
  const number = Number(value)
  return Number.isFinite(number) ? number : 0
}

function lineShape(first, second) {
  return {x1: first[0], y1: first[1], x2: second[0], y2: second[1]}
}

function drawingStyle(drawing) {
  return {
    stroke: drawing.style?.color || '#f0a020',
    lineWidth: finite(drawing.style?.lineWidth) || 1.5,
    lineDash: drawing.style?.lineDash || undefined,
    opacity: drawing.style?.opacity === undefined ? 0.95 : finite(drawing.style.opacity),
  }
}

function textStyle(text, x, y, color) {
  return {
    text,
    x,
    y,
    fill: color,
    font: '12px sans-serif',
    backgroundColor: 'rgba(20,20,20,.72)',
    padding: [3, 5],
  }
}

function renderDrawing(drawing, params, api) {
  const points = drawing.points.map(point => api.coord([point.time, point.value]))
  if (points.some(point => !Array.isArray(point) || point.some(value => !Number.isFinite(value)))) return null
  const style = drawingStyle(drawing)
  const coordinate = params.coordSys
  const right = coordinate.x + coordinate.width
  const left = coordinate.x

  if (drawing.type === 'horizontal_line') {
    return {type: 'line', shape: lineShape([left, points[0][1]], [right, points[0][1]]), style}
  }

  if (drawing.type === 'ray') {
    const [first, second] = points
    const deltaX = second[0] - first[0]
    const extended = deltaX === 0
      ? second
      : [right, first[1] + (second[1] - first[1]) * (right - first[0]) / deltaX]
    return {type: 'line', shape: lineShape(first, extended), style}
  }

  if (drawing.type === 'wave') {
    return {type: 'polyline', shape: {points}, style: {...style, fill: null}}
  }

  if (drawing.type === 'fibonacci_retracement') {
    const [first, second] = drawing.points
    const x1 = Math.min(points[0][0], points[1][0])
    const x2 = Math.max(points[0][0], points[1][0])
    const children = fibonacciLevels.flatMap(level => {
      const value = first.value + (second.value - first.value) * level
      const y = api.coord([first.time, value])[1]
      return [
        {type: 'line', shape: lineShape([x1, y], [x2, y]), style: {...style, opacity: 0.72}},
        {type: 'text', style: textStyle(`${(level * 100).toFixed(1)}% ${value.toFixed(3)}`, x2 + 4, y - 7, style.stroke)},
      ]
    })
    return {type: 'group', children}
  }

  const [first, second] = points
  const children = [{type: 'line', shape: lineShape(first, second), style}]
  if (drawing.type === 'measure') {
    const start = drawing.points[0].value
    const end = drawing.points[1].value
    const delta = end - start
    const percent = start === 0 ? 0 : delta / start * 100
    children.push({
      type: 'text',
      style: textStyle(`${delta >= 0 ? '+' : ''}${delta.toFixed(3)} (${percent >= 0 ? '+' : ''}${percent.toFixed(2)}%)`, (first[0] + second[0]) / 2, (first[1] + second[1]) / 2 - 10, style.stroke),
    })
  }
  return {type: 'group', children}
}

export function buildDrawingSeries(drawings = []) {
  return activeDrawings(drawings).map(drawing => ({
    id: `drawing:${drawing.id}`,
    name: `绘图:${drawing.type}`,
    type: 'custom',
    coordinateSystem: 'cartesian2d',
    xAxisIndex: 0,
    yAxisIndex: 0,
    data: [0],
    renderItem: (params, api) => renderDrawing(drawing, params, api),
    silent: true,
    animation: false,
    tooltip: {show: false},
    z: 100,
  }))
}
